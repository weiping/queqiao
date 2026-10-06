// magpie — one place to pick every agent's model.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/yetone/magpie/internal/agent"
	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/claudebridge"
	"github.com/yetone/magpie/internal/davsync"
	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/imagemcp"
	"github.com/yetone/magpie/internal/netproxy"
	"github.com/yetone/magpie/internal/proc"
	"github.com/yetone/magpie/internal/profile"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/sessions"
	"github.com/yetone/magpie/internal/settings"
	"github.com/yetone/magpie/internal/tui"
	"github.com/yetone/magpie/internal/update"
)

var version = "dev"

const usage = `queqiao — one place to pick every agent's model

  queqiao                        open the app: a window plus a menu bar icon
  queqiao tray                   start in the menu bar only
  queqiao panel                  open the menu bar icon's quick panel, or close it
  queqiao autostart [on|off]     open queqiao (in the menu bar) when you log in, or say whether it does
  queqiao tui                    the same thing, in the terminal (serves the gateway while open when no queqiao does)
  queqiao web [--addr host:port] [--lan] [--no-open] [--gateway]
                                  the app's window in a browser, with the gateway (no desktop needed: WSL, a server over SSH)
                                  a new key each run; MAGPIE_WEB_KEY (16+ letters, digits, - . _ ~) keeps one, signed in for 400 days
                                  --gateway: gateway mode, no Agents, Sessions or Library (on by itself with no agents here; Settings › General turns it off)
  queqiao ls                     list detected agents and their settings
  queqiao <agent>                show one agent
  queqiao <agent> <model>        set an agent's model   e.g. queqiao claude deepseek/deepseek-chat
  queqiao <agent> <field> <value>  set another field   e.g. queqiao codex effort high
  queqiao <agent> default        take queqiao out: the agent back on what it had before
  queqiao <agent> <field> default  that field back to the agent's own default

  queqiao save <name>            snapshot every agent's settings as a profile
  queqiao use <name>             apply a profile
  queqiao profiles               list profiles
  queqiao rm <name>              delete a profile

  queqiao backup [--no-keys] [--no-library] [file]  providers, keys, settings, profiles, agent models and the library in one file, sealed with a passphrase
  queqiao restore [--no-agents] [--no-library] <file> put a backup in on this machine
  queqiao webdav [on <address>|set k=v…|now|off]    the same, kept the same on every computer through a WebDAV folder (queqiao webdav help)
  queqiao s3 [on s3://<bucket>[/<prefix>]|set k=v…|now|off] the same through an S3-compatible bucket: AWS, R2, B2, MinIO… (queqiao s3 help)

  queqiao library [sync|instructions|mcp|skill] the instructions, MCP servers and skills written into every agent (queqiao library help)

  queqiao providers              list your providers: host, key, models, who uses them
  queqiao presets                the vendors queqiao knows: add one with just a key
  queqiao provider add <preset> <key> e.g. queqiao provider add deepseek sk-…
  queqiao provider add <name> k=v…    a custom vendor (queqiao provider for the fields)
  queqiao provider key|models|test|rm <id>
  queqiao provider fallback <id> <provider/model>… use these when it's out of quota or down
  queqiao import [-y] <link>     add the provider a queqiao://import?… link describes
  queqiao models [<agent>]       every model agents can pick, as provider/model; an agent's, and why others aren't
  queqiao model name <provider/model> <name>|--reset     the name a model goes by, everywhere
  queqiao model efforts <provider/model> <l>,<l>|--reset the reasoning levels a model offers (queqiao model help)
  queqiao visible [<agent> <family|provider|group>,… | all]
                                  which models an agent is shown: families (queqiao provider/group set <id> family=…)
  queqiao search [add <api> <key>|rm <api>] Tavily, Brave, Exa, Firecrawl or SearXNG for web search when no provider can search
  queqiao groups                 routing groups: several models agents pick as one, group/<id>
  queqiao group add <name> models=<m1>,<m2> [routing=smart|order|rotate|usage|pace] [stays=auto|session|turn|off]
  queqiao group <id> | set <id> k=v… | rm <id> show, change or remove one (queqiao group help for more)
  queqiao accounts [agent] [--json]  every subscription queqiao knows, with each one's allowance used and when it resets
  queqiao accounts add <agent>   sign in to one more Claude, ChatGPT or Google (Gemini CLI, Antigravity) subscription
  queqiao accounts add copilot [--host <name>.ghe.com] one more Copilot account, on github.com or an enterprise's GHE.com
  queqiao accounts switch <agent> <email> sign the agent in to another of them
  queqiao accounts refresh       renew the saved ChatGPT sign-ins now (the gateway does it daily)
  queqiao accounts checkin       WorkBuddy's daily check-in (签到) for each WorkBuddy account, now (Settings can do it daily)
  queqiao accounts project <gemini|antigravity> <email> <project> the Google Cloud project a Google account's requests go to
  queqiao plugin [add <package>|rm|update|on|off|login <provider>|logout <provider>]
                                  OpenCode provider plugins and pi packages: subscriptions signed in to, and served, through a plugin
  queqiao plugin move|migrate <subscription> run a built-in subscription's accounts on its community plugin
  queqiao plugin move-back|unmigrate <subscription> go back to the built-in, with its accounts

  queqiao serve                  run the gateway alone (the app runs it too)
  queqiao healthcheck            exit 0 when the gateway answers (a container's HEALTHCHECK)
  queqiao gateway-key list|add <name>|rotate <id>|remove <id> manage the keys clients use to call a shared gateway
  queqiao gateway-key limit <id> [off|day|week|month --tokens N --cost USD --cache-reads] a key's own limit, and what it used
  queqiao gateway-key models <id> [all|<provider>/<model>|<provider>/* ...] the models a key may use, every one unless it names some
  queqiao mcp image              the image and video generation MCP server an agent is given from the library (stdio)
  queqiao usage [today|7d|30d|all] tokens and cost per agent, model and subscription account (30d)
  queqiao usage --csv [--account <name>] [today|7d|30d|all] every request as CSV (or one account's): the model asked for, sent and served, tokens, cost, time, status, account
  queqiao sessions [--model <m>] [--folder <f>] [--json] the latest Claude Code, Codex, OpenCode and Pi sessions, with what each cost
  queqiao sessions --days N|today|all [--model <m>] [--folder <f>] [--json]
                                  what every session spent, day by day, with the top models and folders (7 days)
  queqiao quota [<provider>] [--json]  what is left of every subscription, plan and key balance
  queqiao quota wait <provider|account> [--timeout <d>] [--quiet]
                                  block until that subscription (any of its accounts) or account has allowance again
  queqiao quota history [<provider|account>] [--days N] [--json]
                                  each window's readings over time, kept 45 days
  queqiao sync                   refresh the model catalog and vendor model lists
  queqiao agents                 list every supported agent
  queqiao update [check] [--proxy <url>] [--mirror <prefix>]
                                  install the newest release (check: only say if there is one); --proxy: an
                                  http(s):// or socks5:// proxy for it; --mirror: a GitHub download mirror put
                                  before the github.com URL (none unless given; still checked against usemagpie.ai's SHA-256)
  queqiao update mirror [<prefix>|off]  the mirror every update, the app's own too, is downloaded through
  queqiao update auto [on|off] [30m|1h|6h|24h]  whether the app looks for updates by itself, and how often (6h)

agents: claude (cc), codex, gemini, opencode (oc), mimocode, pi, goose, cursor, zed, copilot, crush, aside
`

var (
	bold  = lipgloss.NewStyle().Bold(true)
	muted = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#8B8F98", Dark: "#7C8290"})
	faint = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#C4C7CE", Dark: "#4A4F5A"})
	green = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#0F9D58", Dark: "#7EE2A8"})
)

func main() {
	appdir.SetName("queqiao")
	// before anything reads or writes a file: no home, or a relative one,
	// would put the agents' configs and magpie's keys under the working
	// folder. What needs no file still answers (queqiao version in a
	// container or a script without HOME): run makes magpie's folders first.
	ignored, err := appdir.CheckEnv()
	if err != nil {
		if len(os.Args) > 1 {
			switch os.Args[1] {
			case "-v", "--version", "version":
				fmt.Println("queqiao", version)
				return
			case "-h", "--help", "help":
				fmt.Print(usage)
				return
			}
		}
		fmt.Fprintln(os.Stderr, "queqiao:", err)
		os.Exit(1)
	}
	slices.Sort(ignored)
	for _, v := range ignored {
		fmt.Fprintf(os.Stderr, "queqiao: ignoring %s: not an absolute path (the programs queqiao starts still get it)\n", v)
	}
	if provider.TookOpenedURL(os.Args[1:]) {
		// Claude Code, signing in for magpie, handed over the page to open
		return
	}
	endProbesOnSignal()
	gateway.Version = version
	netproxy.Install()
	update.GUI = hasGUI
	err = run(os.Args[1:])
	proc.EndProbes() // a CLI still being asked something isn't left to init
	sessions.Saved() // the session index kept, for the next run
	if err != nil {
		// a command with exit codes of its own (quota wait) says which
		code := 1
		var e exitError
		if errors.As(err, &e) {
			code = e.code
		}
		if msg := err.Error(); msg != "" {
			fmt.Fprintln(os.Stderr, "queqiao:", msg)
		}
		os.Exit(code)
	}
}

// runTUI runs the TUI, which quits on Ctrl+C and SIGTERM itself once it
// has started; it asks CLIs first, and a signal then ends those.
func runTUI() error {
	return tuiRun(ownSignals)
}

// tuiRun is tui.Run; a var so tests can stand in for it.
var tuiRun = tui.Run

func run(args []string) error {
	if len(args) > 0 && args[0] == "healthcheck" {
		return healthcheck() // every few seconds in a container: nothing else
	}
	if len(args) == 2 && args[0] == agent.DryRunArg {
		// an agent disconnected on a copy of its files under a temporary
		// home, for the Agents page to show what disconnecting changes
		// (agent.DisconnectPreview)
		return agent.DryRun(args[1])
	}
	// started by an update, the magpie it replaces goes first: the moves
	// below write the files it may still be writing
	update.AwaitPredecessor()
	makeDirs()
	settings.Migrate()
	// the providers and settings read once for every agent's fields, which
	// the moves below look at (a write among them reads them again)
	release := provider.Hold()
	agent.RenameLegacy()
	agent.MoveCursorEfforts()
	agent.MoveAntigravityEfforts()
	agent.MoveOffAccountIDs()
	release()
	// a provider added, edited or removed, or a list fetched anew, reaches
	// the model lists agents keep in files of their own
	catalog.Changed = agent.SyncCatalog
	// a model Claude Code names that magpie doesn't serve goes to the one
	// it is set to use for that tier
	gateway.StandIn = agent.StandIn
	// the setup kept the same on every computer, by whichever serves
	gateway.WhileServing = append(gateway.WhileServing, davsync.Run)
	// and dsh's patch lists, which dsh reads live: a route left behind by
	// something else writing the file fails every session there until
	// magpie writes its own list again
	gateway.WhileServing = append(gateway.WhileServing, agent.KeepDshWired)
	// and Cursor Private Inference's variables, which the Mac's launchd
	// forgets at a restart, for the gateway's address now
	gateway.WhileServing = append(gateway.WhileServing, agent.KeepCursorLocalEnv)
	// and the request archive, when it is on, goes to the bucket sync is to
	gateway.ArchiveBucket = func() (gateway.Putter, bool) {
		if b, ok := davsync.S3Bucket(); ok {
			return b, true
		}
		return nil, false
	}
	beforeCommand(args)
	if len(args) == 0 {
		if hasGUI {
			return runGUI(true, "")
		}
		return runTUI()
	}
	// a magpie:// link the system handed over (Windows, Linux): the app
	// opens it for the user to confirm
	if strings.HasPrefix(strings.ToLower(args[0]), "queqiao:") {
		if !hasGUI {
			return importCmd(args)
		}
		return runGUI(false, args[0])
	}
	switch args[0] {
	case "tui":
		return runTUI()
	case "web":
		return webCmd(args[1:])
	case "app", "gui":
		// `queqiao gui settings`: the window on that tab, as a restart to
		// update from it comes back (update.RelaunchArgs)
		if len(args) > 1 {
			return runWindow(args[1])
		}
		return runGUI(true, "")
	case "tray":
		return runGUI(false, "")
	case "-Embedding":
		// Windows starting magpie for a click on one of its notifications
		// (a usage alert, #368) left in the Action Center after it quit:
		// the window, on the Usage page
		return runWindow("usage")
	case "panel":
		return runPanel()
	case "autostart":
		return autostartCmd(args[1:])
	case "-h", "--help", "help":
		fmt.Print(usage)
		return nil
	case "-v", "--version", "version":
		fmt.Println("queqiao", version)
		return nil
	case "ls", "list":
		// in the order the app lists them; those hidden there come last, dimmed
		shown, hidden := settings.Arrange(settings.Load(), agent.Detected(), func(a *agent.Agent) string { return a.ID })
		return list(append(shown, hidden...), true, len(shown))
	case "agents":
		return list(agent.All(), false, -1)
	case "sync":
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := catalog.Sync(ctx); err != nil {
			return err
		}
		fmt.Println(green.Render("✓"), "catalog saved to", catalog.CachePath())
		refreshLive(ctx)
		return nil
	case "save", "use", "rm", "profiles":
		return profiles(args)
	case "import":
		return importCmd(args[1:])
	case "providers":
		return providers()
	case "presets":
		return presets()
	case "provider":
		return providerCmd(args)
	case "models":
		return models(args[1:])
	case "model":
		return modelCmd(args[1:])
	case "visible":
		return visibleCmd(args[1:])
	case "search":
		return searchCmd(args[1:])
	case "groups":
		return groups()
	case "group":
		return groupCmd(args)
	case "serve":
		return serve()
	case "gateway-key":
		return gatewayKeys(args)
	case "accounts", "account":
		return accountsCmd(args)
	case "usage":
		return usageCmd(args)
	case "sessions":
		return sessionsCmd(args)
	case "quota", "quotas":
		return quotaCmd(args)
	case "update":
		return updateCmd(args)
	case "router":
		return routerCmd(args[1:])
	case "hook":
		return hookCmd(args[1:])
	case "library", "lib":
		return libraryCmd(args)
	case "backup":
		return backupCmd(args[1:])
	case "restore":
		return restoreCmd(args[1:])
	case "webdav", "dav":
		return webdavCmd(args[1:])
	case "plugin", "plugins":
		return pluginCmd(args)
	case "s3":
		return s3Cmd(args[1:])
	case "mcp":
		return imagemcp.Run(args[1:])
	case "claude-mcp-helper": // internal: stdio MCP subprocess spawned by Claude Code
		return claudebridge.RunMCP(args[1:])
	}

	a, err := agent.Find(args[0])
	if err != nil {
		return err
	}
	if len(a.Fields) == 0 && a.Import != nil {
		// `queqiao cindy`: it takes magpie through its own link, confirmed there
		if len(args) > 1 {
			link := a.Import()
			openInBrowser(link)
			fmt.Println(green.Render("✓"), bold.Render(a.Name), muted.Render("opened to add queqiao — confirm it there"))
			fmt.Println(muted.Render("  " + link))
			return nil
		}
	}
	switch len(args) {
	case 1:
		return list([]*agent.Agent{a}, true, -1)
	case 2:
		if args[1] == "default" {
			if a.Wired() {
				return disconnect(a)
			}
			return set(a, a.Fields[0].Key, "")
		}
		// `queqiao codex xhigh`: a bare value that belongs to a non-model field
		// (effort levels, for instance) is routed there; anything else is a model.
		if f := fieldForValue(a, args[1]); f != nil {
			return set(a, f.Key, args[1])
		}
		return set(a, a.Fields[0].Key, args[1])
	case 3:
		if args[2] == "default" {
			args[2] = ""
		}
		return set(a, args[1], args[2])
	}
	return fmt.Errorf("too many arguments\n\n%s", usage)
}

func set(a *agent.Agent, key, value string) error {
	f := a.Field(key)
	if f == nil {
		var keys []string
		for _, f := range a.Fields {
			keys = append(keys, f.Key)
		}
		return fmt.Errorf("%s has no field %q (fields: %s)", a.Name, key, strings.Join(keys, ", "))
	}
	value, err := a.Spell(f.Key, value)
	if err != nil {
		return err
	}
	before := f.Get()
	if err := a.Apply(f.Key, value); err != nil {
		return err
	}
	// what the config reads now, not what was asked: an agent may name the
	// model under a provider of its own (OpenCode's magpie-relay/…), and a
	// value it had already is said to be so
	now := f.Get()
	shown := now
	if value == "" || now == "" {
		shown = muted.Render("default")
	}
	if now == before {
		shown += " " + muted.Render("(unchanged)")
	}
	fmt.Println(green.Render("✓"), bold.Render(a.Name), muted.Render(f.Label), shown)
	if a.Notice != nil {
		if n := a.Notice(); n != "" {
			fmt.Println(muted.Render("  ↻ " + n))
		}
	}
	return nil
}

// disconnect is `queqiao <agent> default` on an agent magpie is wired into:
// the Agents page's Disconnect, which puts back what the user had before
// magpie — Claude Code's own model, its endpoint — where a field's default
// leaves the agent as installed (__jingling on X: queqiao claude default
// took the model they had set away with magpie's)
func disconnect(a *agent.Agent) error {
	before := a.Values()
	if err := a.Disconnect(); err != nil {
		return err
	}
	now := a.Values()
	for _, f := range a.Fields {
		if before[f.Key] == now[f.Key] {
			continue
		}
		shown := now[f.Key]
		if shown == "" {
			shown = muted.Render("default")
		}
		fmt.Println(green.Render("✓"), bold.Render(a.Name), muted.Render(f.Label), shown)
	}
	fmt.Println(muted.Render("  disconnected from queqiao, back to what it had before"))
	if a.Notice != nil {
		if n := a.Notice(); n != "" {
			fmt.Println(muted.Render("  ↻ " + n))
		}
	}
	return nil
}

func fieldForValue(a *agent.Agent, v string) *agent.Field {
	vals := a.Values()
	// the agent's suffix after a model (omp's ":max") aside: a role on the
	// same model at that level offers it as typed, and would take it. A list
	// of models no picker offers: it is for the model
	if a.SplitSuffix != nil {
		m, _, one := a.SplitSuffix(v)
		if !one {
			return nil
		}
		v = m
	}
	// a model stays with the model, even where other fields offer it too
	// (Claude Code's opus/sonnet/haiku/fable)
	for _, o := range a.Fields[0].Options(vals) {
		if o.Value == v {
			return nil
		}
	}
	for i := 1; i < len(a.Fields); i++ {
		for _, o := range a.Fields[i].Options(vals) {
			if o.Value == v {
				return &a.Fields[i]
			}
		}
	}
	return nil
}

// list prints the agents; those from dimFrom on (when not -1) are the ones
// hidden in the app, and are dimmed.
func list(agents []*agent.Agent, detectedOnly bool, dimFrom int) error {
	if len(agents) == 0 {
		return fmt.Errorf("no supported agents found on this machine")
	}
	type row struct{ name, vals, path string }
	var rows []row
	nameW, valW := 0, 0
	for i, a := range agents {
		r := row{name: a.Name, path: tilde(a.Path)}
		if !detectedOnly && !a.Detected() {
			r.name = faint.Render(a.Name)
			r.vals = faint.Render("not detected")
			r.path = ""
		} else {
			vals := a.Values()
			dim := dimFrom >= 0 && i >= dimFrom
			label, value := muted, lipgloss.NewStyle()
			if dim {
				label, value = faint, faint
			}
			var parts []string
			for _, f := range a.Fields {
				v := vals[f.Key]
				if v == "" && f.Quiet {
					continue
				}
				if v == "" {
					v = faint.Render("—")
				} else {
					v = value.Render(v)
				}
				if f.Label == "model" {
					parts = append(parts, v)
				} else {
					parts = append(parts, label.Render(f.Label)+" "+v)
				}
			}
			r.name = bold.Render(a.Name)
			if dim {
				r.name = faint.Render(a.Name) + " " + faint.Render("hidden")
			}
			r.vals = strings.Join(parts, label.Render("  ·  "))
			if a.Import != nil {
				if a.Added != nil && a.Added() {
					r.vals = value.Render("queqiao added")
				} else {
					r.vals = label.Render("queqiao "+a.ID+" add") + faint.Render("  to add queqiao")
				}
			}
		}
		nameW = max(nameW, lipgloss.Width(r.name))
		valW = max(valW, lipgloss.Width(r.vals))
		rows = append(rows, r)
	}
	for _, r := range rows {
		fmt.Printf("  %s  %s  %s\n", pad(r.name, nameW), pad(r.vals, valW), faint.Render(r.path))
	}
	return nil
}

func tilde(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}

func profiles(args []string) error {
	switch args[0] {
	case "profiles":
		ps, err := profile.Load()
		if err != nil {
			return err
		}
		if len(ps) == 0 {
			fmt.Println(muted.Render("no profiles yet · queqiao save <name>"))
			return nil
		}
		for _, n := range profile.Names(ps) {
			fmt.Printf("  %s  %s\n", bold.Render(n), muted.Render(profile.LongSummary(ps[n])))
		}
		return nil
	case "save":
		if len(args) < 2 {
			return fmt.Errorf("usage: queqiao save <name>")
		}
		p, err := profile.Snapshot()
		if err != nil {
			return err
		}
		if err := profile.Save(args[1], p); err != nil {
			return err
		}
		fmt.Println(green.Render("✓"), "saved profile", bold.Render(args[1]))
		if p.Library != nil {
			fmt.Println(" ", muted.Render("with the "+p.Library.Summary()))
		}
		return nil
	case "use":
		if len(args) < 2 {
			return fmt.Errorf("usage: queqiao use <name>")
		}
		ps, err := profile.Load()
		if err != nil {
			return err
		}
		p, ok := ps[args[1]]
		if !ok {
			return fmt.Errorf("no profile named %q", args[1])
		}
		a, err := profile.Apply(p)
		if err != nil {
			return err
		}
		fmt.Println(green.Render("✓"), "applied", bold.Render(args[1]), muted.Render(fmt.Sprintf("(%d changed)", a.Changed)))
		for _, line := range profile.Report(a) {
			fmt.Println(" ", muted.Render(line))
		}
		return nil
	case "rm":
		if len(args) < 2 {
			return fmt.Errorf("usage: queqiao rm <name>")
		}
		if err := profile.Delete(args[1]); err != nil {
			return err
		}
		fmt.Println(green.Render("✓"), "deleted profile", bold.Render(args[1]))
		return nil
	}
	return nil
}

func pad(s string, w int) string {
	if n := w - lipgloss.Width(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}
