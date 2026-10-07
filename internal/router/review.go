package router

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ReviewQuestion is what the end-of-turn reviewer sees (§3.4): the user's
// request, the assistant's final answer, the tool stats as context, and
// the tier the turn ran on.
type ReviewQuestion struct {
	Request      string
	Answer       string
	ToolCalls    int
	ToolFailures int
	Tier         Tier
}

// reviewSchema is §4's structured-output schema for a plain-model
// reviewer: how unresolved the answer is, and how sure the model is.
const reviewSchema = `"response_format": {
    "type": "json_schema",
    "json_schema": {
      "name": "review_verdict",
      "strict": true,
      "schema": {
        "type": "object",
        "properties": {
          "unresolved": { "type": "number", "description": "How likely the answer leaves the request unresolved: from 0 (it is resolved) to 1 (it is not)." },
          "confidence": { "type": "number", "description": "How sure you are of that, from 0 to 1." }
        },
        "required": ["unresolved", "confidence"],
        "additionalProperties": false
      }
    }
  }`

// truncateAnswer cuts an over-long answer down to max runes, keeping the
// first 2000 (what was asked and started) and the last max-2000 (how it
// ended) — §3.3.
func truncateAnswer(s string, max int) string {
	r := []rune(s)
	if max <= 0 || len(r) <= max {
		return s
	}
	head := 2000
	if head > max {
		head = max
	}
	tail := max - head
	if tail <= 0 {
		return string(r[:head])
	}
	return string(r[:head]) + string(r[len(r)-tail:])
}

// Review asks the classifier whether the turn's answer left the request
// unresolved (§3.4).
func (c *classifier) Review(ctx context.Context, q ReviewQuestion) (*ReviewVerdict, error) {
	if c.cfg.Classifier == "" {
		return nil, fmt.Errorf("router: no classifier configured")
	}
	if c.jev {
		return c.reviewJev(ctx, q)
	}
	return c.reviewPlain(ctx, q)
}

func (c *classifier) reviewJev(ctx context.Context, q ReviewQuestion) (*ReviewVerdict, error) {
	state := map[string]any{
		"request":       q.Request,
		"answer":        q.Answer,
		"tool_calls":    q.ToolCalls,
		"tool_failures": q.ToolFailures,
		"tier":          string(q.Tier),
	}
	questions := map[string]any{"unresolved": map[string]any{
		"type": "noul",
		"instructions": "`request` is what a user asked a coding assistant and `answer` is the " +
			"assistant's final reply for that turn. The answer leaves the request unresolved: it is " +
			"wrong, incomplete, gives up, asks the user to do the work, or does something other than " +
			"what was asked.",
	}}
	body, err := json.Marshal(map[string]any{
		"model": "jev-latest", "state": state, "questions": questions,
	})
	if err != nil {
		return nil, err
	}
	reply, err := c.askOnce(ctx, string(body))
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Answers struct {
			Unresolved struct {
				Noul float64 `json:"noul"`
			} `json:"unresolved"`
		} `json:"answers"`
	}
	if err := json.Unmarshal([]byte(reply), &parsed); err != nil {
		return nil, fmt.Errorf("router: jev review reply: %v", err)
	}
	return &ReviewVerdict{Unresolved: clamp01(parsed.Answers.Unresolved.Noul), Confidence: 1}, nil
}

func (c *classifier) reviewPlain(ctx context.Context, q ReviewQuestion) (*ReviewVerdict, error) {
	if c.plainSchemaUsable() {
		prompt := "`request` is what a user asked a coding assistant and `answer` is the assistant's final reply for that turn. " +
			"How likely is the answer to leave the request unresolved? 0 means resolved, 1 means not resolved.\n\nrequest:\n" +
			q.Request + "\n\nanswer:\n" + q.Answer
		body := fmt.Sprintf(`{"model":%q,"max_tokens":400,%s,"messages":[{"role":"user","content":%s}]}`,
			c.cfg.Classifier, reviewSchema, jsonString(prompt))
		v, err := c.parseReviewFrom(ctx, body)
		if err == nil {
			c.clearSchemaBlame()
			return v, nil
		}
		if strings.Contains(err.Error(), "structured") {
			c.blameSchema()
		} else {
			c.forbidPlainSchema()
		}
	}
	return c.reviewPlainOld(ctx, q)
}

func (c *classifier) parseReviewFrom(ctx context.Context, body string) (*ReviewVerdict, error) {
	reply, err := c.askOnce(ctx, body)
	if err != nil {
		return nil, err
	}
	var res struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(reply), &res); err != nil || len(res.Choices) == 0 {
		return nil, fmt.Errorf("router: review structured reply: %v", err)
	}
	var v struct {
		Unresolved float64 `json:"unresolved"`
		Confidence float64 `json:"confidence"`
	}
	if err := json.Unmarshal([]byte(res.Choices[0].Message.Content), &v); err != nil {
		return nil, fmt.Errorf("router: review structured content: %v", err)
	}
	return &ReviewVerdict{Unresolved: clamp01(v.Unresolved), Confidence: clamp01(v.Confidence)}, nil
}

// reviewPlainOld is the fallback for a model that refused structured
// output: ask for a single number, and take the answer at face value.
func (c *classifier) reviewPlainOld(ctx context.Context, q ReviewQuestion) (*ReviewVerdict, error) {
	prompt := "Does the following reply leave the user's request unresolved (wrong, incomplete, gives up, " +
		"asks the user to do the work, or does something else)? Answer with only a number from 0 (resolved) " +
		"to 1 (unresolved).\n\nrequest:\n" + q.Request + "\n\nreply:\n" + q.Answer
	reply, err := c.askPlain(ctx, prompt)
	if err != nil {
		return nil, err
	}
	var f float64
	if _, err := fmt.Sscanf(strings.TrimSpace(reply), "%f", &f); err != nil {
		return nil, fmt.Errorf("router: review fallback reply %q: %v", reply, err)
	}
	return &ReviewVerdict{Unresolved: clamp01(f), Confidence: 1}, nil
}

// reviewReportTimeout is how long a background review may take (§3.2).
func (d *Deps) reviewTimeout() time.Duration {
	ms := d.Config.Review.TimeoutMs
	if ms <= 0 {
		ms = 15000
	}
	return time.Duration(ms) * time.Millisecond
}

// review handles POST /v1/queqiao/review (§3.3): 202 accepted into a
// background review, 204 when the turn does not qualify, 400 on a bad
// request.
func (d *Deps) review(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Session      string `json:"session"`
		Harness      string `json:"harness"`
		TurnID       string `json:"turn_id"`
		Prompt       string `json:"prompt"`
		Answer       string `json:"answer"`
		ToolCalls    int    `json:"tool_calls"`
		ToolFailures int    `json:"tool_failures"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "not a /review request", http.StatusBadRequest)
		return
	}
	if req.Session == "" || req.Prompt == "" || req.Answer == "" {
		http.Error(w, "session, prompt and answer are required", http.StatusBadRequest)
		return
	}
	if !d.reviewWanted(req.Session) {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// The turn the review belongs to: a result landing after the next
	// turn has begun is dropped (PutReview checks this).
	turnAtAccept := d.Sessions.Turn(req.Session)
	q := ReviewQuestion{
		Request:      truncateReviewInput(req.Prompt, 3000, 1000),
		Answer:       truncateAnswer(req.Answer, d.Config.Review.MaxAnswerChars),
		ToolCalls:    req.ToolCalls,
		ToolFailures: req.ToolFailures,
	}
	if prev := d.Sessions.Get(req.Session); prev != nil {
		q.Tier = prev.Tier
	}
	key, session, harness, turnID := req.Session, req.Session, req.Harness, req.TurnID
	go d.runReview(key, session, harness, turnID, turnAtAccept, q)
	w.WriteHeader(http.StatusAccepted)
}

// reviewWanted is §3.2's gate: mode on, not the control arm, not pinned,
// a known session not already at performance.
func (d *Deps) reviewWanted(session string) bool {
	if d.Classify == nil || d.Config.Review.Mode == "" || d.Config.Review.Mode == ReviewOff {
		return false
	}
	if d.Sessions.Pinned(session) || Arm(session, d.Config.Experiment) == "control" {
		return false
	}
	prev := d.Sessions.Get(session)
	return prev != nil && prev.Tier != "" && prev.Tier != TierPerformance
}

// runReview does the background classification and files the result. It
// holds no lock across the call or the event write (LESSONS.md).
func (d *Deps) runReview(key, session, harness, turnID string, turnAtAccept int, q ReviewQuestion) {
	ctx, cancel := context.WithTimeout(context.Background(), d.reviewTimeout())
	defer cancel()
	v, err := d.Classify.Review(ctx, q)
	if err != nil {
		return // a review that fails is a silent 202 (§3.2)
	}
	d.Sessions.PutReview(key, turnAtAccept, *v)
	unresolved, confidence := v.Unresolved, v.Confidence
	d.log(Event{
		Kind: "review", Session: session, Harness: harness, TurnID: turnID,
		Unresolved: &unresolved, ReviewConfidence: &confidence,
		Classifier: d.Config.Classifier,
	})
}

// truncateReviewInput is §5.3's rule for the request text: the first head
// and the last tail runes.
func truncateReviewInput(s string, head, tail int) string {
	r := []rune(s)
	if len(r) <= head+tail {
		return s
	}
	return string(r[:head]) + string(r[len(r)-tail:])
}
