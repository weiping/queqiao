# queqiao SP1-fork Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the magpie fork into a `queqiao` binary that keeps its files in `~/.config/queqiao` and `~/.cache/queqiao`, never checks for updates, never sends usage stats, and still passes every upstream test.

**Architecture:** The smallest set of edits to upstream files that §6.1 of the spec allows. The application name becomes a setting of `internal/appdir` (`SetName`), set once at the top of `main()`, so upstream tests that hard-code `.config/magpie` keep passing untouched. Updates and stats are switched off at their single entry points (`update.LatestIn`, `stats.Run`), keeping the env vars upstream tests use to point them at local servers. Every new test lives in a new file, so merges from `main` never conflict on tests.

**Tech Stack:** Go 1.26 (module `github.com/yetone/magpie`, unchanged), GNU make, GitHub CLI `gh`.

**Spec:** `docs/superpowers/specs/2026-10-02-queqiao-design.md` (§6.1; §11 row `SP1-fork`)

## Global Constraints

- Work on branch `qq/sp1-fork`, cut from `queqiao`, in its own worktree (superpowers:using-git-worktrees); finish with a PR into `queqiao`, merged with a merge commit.
- `go.mod` and every import path stay `github.com/yetone/magpie`.
- `MAGPIE_*` env vars, `X-Magpie-*` headers and `/v1/magpie/*` endpoints keep their names.
- Agent wiring in `internal/agent` is not touched: provider id `magpie`, `[model_providers.magpie]`, `~/.codex/magpie-models.json` (spec §6.1, revised 2026-10-03).
- Upstream files may change only as listed in each task's **Files**; tests go in new files only.
- Test command: `go test -tags nogui ./...` (and `go vet -tags nogui ./...`).
- Release page: `https://github.com/weiping/queqiao/releases`.
- Tags: `queqiao-v<major>.<minor>.<patch>`, cut from `queqiao` (nothing to automate in this plan).
- Known environment failure: `internal/netproxy` `TestBunThroughBridge` fails in sandboxes without `bun` on the baseline too; the PR's GitHub Test workflow is the authority.

## Review Focus

1. A package that computes a config path during `init` or in a package-level `var` would capture `magpie` before `main()` renames it → `SetName` must panic loudly, never silently split files across two folders (Task 1 test `TestSetNameAfterUseRefuses`).
2. Portable mode (`data` folder beside the binary) must ignore the name entirely → Task 1 test `TestSetNameKeepsPortable`.
3. `XDG_CONFIG_HOME` / `XDG_CACHE_HOME` set → the renamed folder sits under them (`$XDG_CONFIG_HOME/queqiao`), Task 1.
4. `queqiao update` with no feed must exit 0 with the releases link, not print a network error; `queqiao update check` likewise (Task 3).
5. A user who runs a fresh `queqiao serve` next to an existing `~/.config/magpie` must not have that folder read or written (Task 2 smoke test asserts `magpie` folder is not created).

---

### Task 1: `appdir.SetName`

**Files:**
- Create: `internal/appdir/name.go`
- Modify: `internal/appdir/appdir.go` (the four `"magpie"` literals in `Config`, `Cache`, `SystemCache` only)
- Test: `internal/appdir/name_test.go`

**Interfaces:**
- Produces: `func SetName(name string)` — sets the folder name; panics with `appdir: SetName("<name>") after a folder was handed out as "<old>"` if `Config`, `Cache` or `SystemCache` already returned a path. Default name `magpie`.
- Internal: `func appName() string` — returns the name and marks it handed out; `Config`, `Cache`, `SystemCache` use it in place of the literal.

- [ ] **Step 1: Write the failing tests** in `internal/appdir/name_test.go` (package `appdir`); each test restores `name` and the handed-out flag in `t.Cleanup` and calls `UseExecutable("")` so portable mode is off unless the test sets it.

```go
func TestSetNameRenamesFolders(t *testing.T) {
	x, c := t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", x)
	t.Setenv("XDG_CACHE_HOME", c)
	SetName("queqiao")
	if Config() != filepath.Join(x, "queqiao") || Cache() != filepath.Join(c, "queqiao") {
		t.Fatalf("config %s cache %s", Config(), Cache())
	}
	if d, _ := SystemCache(); filepath.Base(d) != "queqiao" {
		t.Fatalf("system cache %s", d)
	}
}

func TestSetNameDefaultsToMagpie(t *testing.T) // fresh state: filepath.Base(Config()) == "magpie"

func TestSetNameAfterUseRefuses(t *testing.T) {
	_ = Config()
	defer func() {
		if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), `SetName("queqiao")`) {
			t.Fatalf("recovered %v", r)
		}
	}()
	SetName("queqiao")
}

func TestSetNameKeepsPortable(t *testing.T) // UseExecutable(<dir with data/>/magpie); SetName("queqiao"); Config() == that data folder
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test -tags nogui ./internal/appdir/ -run SetName -v`
Expected: FAIL, `undefined: SetName`

- [ ] **Step 3: Implement `SetName` and `appName` in `internal/appdir/name.go`; replace the four `"magpie"` literals in `appdir.go` with `appName()`**

Guard the name and flag with a `sync.Mutex` (the gateway calls `Config` from many goroutines). The package doc comment in `appdir.go` stays as it is.

- [ ] **Step 4: Run the package tests, old and new**

Run: `go test -tags nogui ./internal/appdir/ -v`
Expected: PASS, including upstream `TestFolders`

- [ ] **Step 5: Commit**

```bash
git add internal/appdir/name.go internal/appdir/name_test.go internal/appdir/appdir.go
git commit -m "feat(appdir): SetName decides the config and cache folder name"
```

### Task 2: The `queqiao` binary

**Files:**
- Modify: `main.go` (first statement of `main()`)
- Modify: `Makefile` (targets `build`, `cli`, `app`, `release`, `release-cli`, `clean`)
- Create: `build/queqiao-smoke.sh`

**Interfaces:**
- Consumes: `appdir.SetName` (Task 1).
- Produces: `make cli` → `./queqiao`; `make build` → `./queqiao`; `release-cli` → `dist/queqiao-cli-<os>-<arch>[.exe]`; `release` → `dist/queqiao-<os>-<arch>`. The macOS bundle keeps its internal layout (`magpie.app/Contents/MacOS/magpie`), copied from `$(BIN)`.

- [ ] **Step 1: Write the smoke test `build/queqiao-smoke.sh`** (bash, `set -euo pipefail`)

It takes the binary path as `$1`, makes a temp dir `T`, runs `HOME=$T XDG_CONFIG_HOME=$T/config XDG_CACHE_HOME=$T/cache MAGPIE_ADDR=127.0.0.1:3525 "$1" serve` in the background, waits up to 20 s for `curl -fsS -H 'Authorization: Bearer magpie' http://127.0.0.1:3525/v1/models` to succeed, kills the server, then asserts:
- `$T/config/queqiao` is a directory;
- `$T/config/magpie` and `$T/cache/magpie` do not exist;
- prints `smoke: ok` and exits 0; any failed assertion prints which one and exits 1.

- [ ] **Step 2: Run it against the current build to see it fail**

Run: `make cli && bash build/queqiao-smoke.sh ./queqiao`
Expected: FAIL — `make cli` still writes `./magpie`, so `./queqiao` is missing

- [ ] **Step 3: Change `main.go` and `Makefile`**

`main()` begins with `appdir.SetName("queqiao")` (import `github.com/yetone/magpie/internal/appdir`). In `Makefile` add `BIN ?= queqiao` under `TARGETS` and use `$(BIN)` in place of the output name in the six targets listed under **Files**; leave `release-windows`, `release-linux`, `dev`, `dev-once` and `icons` unchanged.

- [ ] **Step 4: Run the smoke test and the root package tests**

Run: `make cli && bash build/queqiao-smoke.sh ./queqiao && go test -tags nogui . -count=1`
Expected: `smoke: ok`, then `ok  github.com/yetone/magpie`

- [ ] **Step 5: Commit**

```bash
git add main.go Makefile build/queqiao-smoke.sh
git commit -m "feat: build the queqiao binary, keeping its files in ~/.config/queqiao"
```

### Task 3: No automatic updates

**Files:**
- Modify: `internal/update/update.go` (`Site`, `Feed`, `LatestIn`; add `ErrNoFeed`)
- Modify: `update_cli.go` (`updateCmd`, right after `update.Latest`)
- Test: `internal/update/nofeed_test.go`, `queqiao_update_cli_test.go`

**Interfaces:**
- Produces: `var ErrNoFeed = errors.New("queqiao 不自动更新，请从 https://github.com/weiping/queqiao/releases 下载")`; `Feed()` returns `""` unless `MAGPIE_UPDATE_FEED` is set; `LatestIn` returns `ErrNoFeed` without a request when `Feed()` is `""`; `const Site = "https://github.com/weiping/queqiao/releases"`.

- [ ] **Step 1: Write the failing tests**

```go
// internal/update/nofeed_test.go
func TestNoFeedWithoutEnv(t *testing.T) {
	t.Setenv("MAGPIE_UPDATE_FEED", "")
	if Feed() != "" {
		t.Fatalf("feed %q", Feed())
	}
	if _, err := Latest(context.Background()); !errors.Is(err, ErrNoFeed) {
		t.Fatalf("err %v", err)
	}
}

// queqiao_update_cli_test.go (package main), with a local captureStdout(t, func() error) (string, error)
func TestUpdateSaysReleases(t *testing.T) {
	groupsHome(t)
	t.Setenv("MAGPIE_UPDATE_FEED", "")
	for _, args := range [][]string{{"update"}, {"update", "check"}} {
		out, err := captureStdout(t, func() error { return updateCmd(args) })
		if err != nil || !strings.Contains(out, "https://github.com/weiping/queqiao/releases") {
			t.Fatalf("%v: out %q err %v", args, out, err)
		}
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test -tags nogui ./internal/update/ -run NoFeed -v && go test -tags nogui . -run TestUpdateSaysReleases -v`
Expected: FAIL, `undefined: ErrNoFeed`

- [ ] **Step 3: Implement**

In `updateCmd`, when `errors.Is(err, update.ErrNoFeed)`, print `err.Error()` to stdout and return nil. The package comment of `update.go` is left alone.

- [ ] **Step 4: Run the update package, the GUI package and the root package**

Run: `go test -tags nogui ./internal/update/ ./internal/gui/ . -count=1`
Expected: all `ok` (upstream tests set `MAGPIE_UPDATE_FEED` themselves)

- [ ] **Step 5: Commit**

```bash
git add internal/update/update.go internal/update/nofeed_test.go update_cli.go queqiao_update_cli_test.go
git commit -m "feat(update): queqiao has no update feed; point to its releases"
```

### Task 4: No usage stats

**Files:**
- Modify: `internal/stats/stats.go` (`Run`)
- Test: `internal/stats/norun_test.go`

**Interfaces:**
- Produces: `Run(version, what string)` returns at once unless `MAGPIE_STATS_HOST` is set (released or not).

- [ ] **Step 1: Write the failing test**

```go
func TestRunNeedsStatsHost(t *testing.T) {
	t.Setenv("MAGPIE_STATS_HOST", "")
	done := make(chan struct{})
	go func() { Run("1.2.3", "serve"); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run kept going for a release without MAGPIE_STATS_HOST")
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test -tags nogui ./internal/stats/ -run TestRunNeedsStatsHost -v`
Expected: FAIL after 2 s with the message above (and no event leaves the sandbox: set `HOME`/`XDG_CONFIG_HOME` to `t.TempDir()` and `MAGPIE_NO_STATS=1` in the test before calling `Run`, so the upstream loop sends nothing while it fails)

- [ ] **Step 3: Replace `Run`'s first condition with `if os.Getenv("MAGPIE_STATS_HOST") == "" { return }`**

Update `Run`'s doc comment to say queqiao sends nothing unless `MAGPIE_STATS_HOST` is set.

- [ ] **Step 4: Run the package**

Run: `go test -tags nogui ./internal/stats/ -count=1`
Expected: `ok`

- [ ] **Step 5: Commit**

```bash
git add internal/stats/stats.go internal/stats/norun_test.go
git commit -m "feat(stats): send no usage stats to upstream's PostHog"
```

### Task 5: Repository settings, full suite, PR

**Files:** none in the tree (GitHub settings and a PR).

- [ ] **Step 1: Disable the two upstream workflows that must not run here**

Run: `gh workflow disable docker.yml -R weiping/queqiao && gh workflow disable ui-preview.yml -R weiping/queqiao && gh workflow list -R weiping/queqiao --all`
Expected: `Docker` and `UI preview` listed as `disabled_manually`; `Test`, `Release`, `queqiao sync upstream` active. (If `gh` lacks the Actions permission, hand these two clicks to the human partner and record that in the PR description.)

- [ ] **Step 2: Confirm the default branch**

Run: `gh repo view weiping/queqiao --json defaultBranchRef --jq .defaultBranchRef.name`
Expected: `queqiao`

- [ ] **Step 3: Full verification**

Run: `go vet -tags nogui ./... && go test -tags nogui ./... -count=1 && make cli && bash build/queqiao-smoke.sh ./queqiao`
Expected: every package `ok` except the known environmental `TestBunThroughBridge`; `smoke: ok`

- [ ] **Step 4: Push and open the PR**

```bash
git push -u origin qq/sp1-fork
gh pr create -R weiping/queqiao --base queqiao --head qq/sp1-fork \
  --title "SP1: queqiao binary, own config folder, no updates, no stats" \
  --body "Implements §6.1 of docs/superpowers/specs/2026-10-02-queqiao-design.md (plan: docs/superpowers/plans/2026-10-03-queqiao-sp1-fork.md)."
```

- [ ] **Step 5: Wait for the PR's checks and merge with a merge commit**

Run: `gh pr checks <url> --watch --interval 30 && gh pr merge <url> --merge --delete-branch`
Expected: the upstream Test workflow passes on all three systems; PR merged into `queqiao`
