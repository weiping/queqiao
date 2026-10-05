package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// jevClassifier is the cfg.Classifier value naming the Jev two-question
// System One request (§5.3).
const jevClassifier = "typesafe/jev-latest"

// tierOrder is the order the plain-model prompt numbers tiers in (§5.4).
var tierOrder = []Tier{TierFast, TierBalanced, TierPerformance}

// Question is what the classifier is asked (§5.3): the user's message,
// the previous tier ("" first turn), and the agent name.
type Question struct {
	Message      string
	PreviousTier Tier
	Agent        string
	Criteria     map[Tier]string // tier → criteria text (from Config.Tiers)
}

// Classifier picks a tier for a question, or fails (timeout included).
type Classifier interface {
	Classify(ctx context.Context, q Question) (*Verdict, error)
}

// classifier is the built Classifier: a branch (jev or plain) plus the
// timeout and ask callback both branches share.
type classifier struct {
	cfg Config
	ask func(ctx context.Context, model, body string) (string, error)
	jev bool
}

// NewClassifier builds the classifier cfg names: "typesafe/jev-latest"
// (the Jev two-question System One request, §5.3) or any "provider/model"
// (a plain model asked twice: tier as a number, then dissatisfied yes/no,
// §5.4). ask sends one request: it receives the model id and the JSON
// body string, returns the raw response body text.
func NewClassifier(cfg Config, ask func(ctx context.Context, model, body string) (string, error)) Classifier {
	return &classifier{cfg: cfg, ask: ask, jev: cfg.Classifier == jevClassifier}
}

// Classify answers one question, wrapping every ask in the configured
// timeout. Any error fails the classification.
func (c *classifier) Classify(ctx context.Context, q Question) (*Verdict, error) {
	if c.cfg.Classifier == "" {
		return nil, errors.New("router: no classifier configured")
	}
	if c.jev {
		return c.classifyJev(ctx, q)
	}
	return c.classifyPlain(ctx, q)
}

// askOnce sends one request with the classify timeout applied.
func (c *classifier) askOnce(ctx context.Context, body string) (string, error) {
	tctx, cancel := context.WithTimeout(ctx, time.Duration(c.cfg.ClassifyTimeoutMs)*time.Millisecond)
	defer cancel()
	return c.ask(tctx, c.cfg.Classifier, body)
}

// --- §5.3: the Jev two-question request ---

// jevRequest is the body sent to typesafe/jev-latest.
type jevRequest struct {
	Model     string                 `json:"model"`
	State     map[string]string      `json:"state"`
	Questions map[string]jevQuestion `json:"questions"`
}

type jevQuestion struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria,omitempty"`
}

// jevReply is what the Jev response parses into.
type jevReply struct {
	Answers struct {
		Tier struct {
			Choice     string  `json:"choice"`
			Confidence float64 `json:"confidence"`
		} `json:"tier"`
		Dissatisfied struct {
			Noul float64 `json:"noul"`
		} `json:"dissatisfied"`
	} `json:"answers"`
}

func (c *classifier) classifyJev(ctx context.Context, q Question) (*Verdict, error) {
	const tierInstr = "The `message` is what a user asked a coding assistant. " +
		"Which tier can most cheaply handle it well?"
	const carryOn = " A message that only carries on from the turn before " +
		"(go on, yes, do it) is of `previous_tier`."

	state := map[string]string{"message": q.Message, "agent": q.Agent}
	instructions := tierInstr
	if q.PreviousTier != "" {
		state["previous_tier"] = string(q.PreviousTier)
		instructions += carryOn
	}
	criteria := make(map[string]string, len(q.Criteria))
	for tier, text := range q.Criteria {
		criteria[string(tier)] = text
	}
	body, err := json.Marshal(jevRequest{
		Model: "jev-latest",
		State: state,
		Questions: map[string]jevQuestion{
			"tier": {Type: "choice", Instructions: instructions, Criteria: criteria},
			"dissatisfied": {
				Type:         "noul",
				Instructions: "The `message` says the assistant's previous result was wrong, broken, incomplete, or not what the user asked for.",
			},
		},
	})
	if err != nil {
		return nil, err
	}

	reply, err := c.askOnce(ctx, string(body))
	if err != nil {
		return nil, err
	}
	var parsed jevReply
	if err := json.Unmarshal([]byte(reply), &parsed); err != nil {
		return nil, fmt.Errorf("router: jev reply: %v", err)
	}
	v := &Verdict{
		Tier:           Tier(parsed.Answers.Tier.Choice),
		TierConfidence: parsed.Answers.Tier.Confidence,
		Dissatisfied:   parsed.Answers.Dissatisfied.Noul,
	}
	switch v.Tier {
	case TierFast, TierBalanced, TierPerformance:
	default:
		return nil, fmt.Errorf("router: jev chose %q, not a tier", v.Tier)
	}
	return v, nil
}

// --- §5.4: a plain model asked twice ---

// plainRequest is the chat-completions body a plain model gets.
type plainRequest struct {
	Model     string         `json:"model"`
	Messages  []plainMessage `json:"messages"`
	MaxTokens int            `json:"max_tokens"`
}

type plainMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// askPlain sends one user message to the plain classifier model.
func (c *classifier) askPlain(ctx context.Context, prompt string) (string, error) {
	body, err := json.Marshal(plainRequest{
		Model:     c.cfg.Classifier,
		Messages:  []plainMessage{{Role: "user", Content: prompt}},
		MaxTokens: 400, // reasoning models think before they answer; the
		// classify timeout, not this cap, is what bounds the wait
	})
	if err != nil {
		return "", err
	}
	return c.askOnce(ctx, string(body))
}

func (c *classifier) classifyPlain(ctx context.Context, q Question) (*Verdict, error) {
	var prompt strings.Builder
	prompt.WriteString("You route a user's message to a coding assistant. " +
		"Given numbered tiers and the user's message, answer with only the number of the tier that fits best.\n")
	for i, tier := range tierOrder {
		fmt.Fprintf(&prompt, "%d. %s\n", i+1, q.Criteria[tier])
	}
	prompt.WriteString("\nMessage:\n" + q.Message)

	reply, err := c.askPlain(ctx, prompt.String())
	if err != nil {
		return nil, err
	}
	v := &Verdict{}
	if i := strings.IndexAny(reply, "123"); i >= 0 {
		v.Tier = tierOrder[reply[i]-'1']
		v.TierConfidence = 1
	}

	if q.PreviousTier == "" {
		return v, nil // no previous result to be dissatisfied with
	}
	reply, err = c.askPlain(ctx, "Does the following message from a user say the assistant's previous answer was wrong, broken, incomplete, or not what they asked for? Answer yes or no only.\n\nMessage:\n"+q.Message)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(reply)), "yes") {
		v.Dissatisfied = 1
	}
	return v, nil
}
