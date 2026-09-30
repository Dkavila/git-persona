<p align="center">
  <strong>git-persona</strong><br>
  <em>Switch between multiple Git and SSH identities without ever touching <code>~/.ssh/config</code>.</em>
</p>

<p align="center">
  <a href="https://github.com/Dkavila/git-persona/actions/workflows/ci.yml"><img src="https://github.com/Dkavila/git-persona/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://pkg.go.dev/github.com/Dkavila/git-persona"><img src="https://pkg.go.dev/badge/github.com/Dkavila/git-persona.svg" alt="Go Reference"></a>
  <a href="https://github.com/Dkavila/git-persona/releases/latest"><img src="https://img.shields.io/github/v/release/Dkavila/git-persona" alt="Release"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache%202.0-blue.svg" alt="License"></a>
</p>

<p align="center">
  <a href="#the-problem">Problem</a> •
  <a href="#how-it-works">How it works</a> •
  <a href="#installation">Installation</a> •
  <a href="#commands">Commands</a> •
  <a href="#design-notes">Design notes</a>
</p>

---

```console
$ git-persona list
   NAME      EMAIL              KEY
*  work      dev@acme-corp.com  ~/.ssh/id_ed25519_work
   personal  me@example.com     ~/.ssh/id_ed25519_personal

$ git-persona use personal
Now using "personal" (me@example.com)

$ git-persona verify
STATUS   PROFILE   ACCOUNT   TOOK
OK       work      acme-bot  412ms
OK       personal  octocat   389ms
```

## The problem

You have a work GitHub account and a personal one. Both authenticate over SSH.

The usual fix is a hand-maintained `~/.ssh/config` full of `Host github-work`
aliases, which forces you to rewrite every remote URL to
`git@github-work:org/repo.git`. Clone something with the normal URL and you
silently push as the wrong person — and only find out when the commit shows up
under the wrong name.

## How it works

`git-persona` never touches `~/.ssh/config` and never rewrites a remote. It
writes three keys into your **global** Git config:

```ini
[user]
    name = personal
    email = me@example.com
[core]
    sshCommand = ssh -i "/home/user/.ssh/id_ed25519_personal" -o IdentitiesOnly=yes
```

`core.sshCommand` tells Git which SSH binary and which key to use for every
transport operation. `IdentitiesOnly=yes` stops the agent from offering other
keys first, which is the usual cause of authenticating as the wrong account.

Remotes stay as `git@github.com:org/repo.git`. Each profile gets its own
ed25519 key pair, generated on `add`.

## Installation

### Windows

Download `GitPersona_Installer.exe` from the
[latest release](https://github.com/Dkavila/git-persona/releases/latest).

It installs per-user — no administrator prompt — adds itself to your `PATH`,
and provides the short alias `gitp`.

### Go toolchain (any platform)

```console
go install github.com/Dkavila/git-persona/cmd/git-persona@latest
```

### From source

```console
git clone https://github.com/Dkavila/git-persona.git
cd git-persona
make build
```

Requires Go 1.27+, plus `git` and `ssh-keygen` on your `PATH`.

## Commands

```console
$ git-persona --help
git-persona switches Git identities by writing user.name, user.email
and core.sshCommand into your global Git config, so your ~/.ssh/config
is never touched.

Usage:
  git-persona [command]

Available Commands:
  add         Register a new profile and generate its ed25519 key
  clean       Unset local Git identity so the global profile applies again
  list        List registered profiles and show the active one
  remove      Remove a profile, optionally deleting its SSH key
  rewrite     Rewrite commit authorship to a registered profile
  use         Apply a profile to the global Git configuration
  verify      Check every profile's SSH connection to GitHub, concurrently
```

| Command | What it does |
|---|---|
| [`add`](#add) | Register a profile and generate its key |
| [`use`](#use) | Apply a profile to the global Git config |
| [`list`](#list) | Show profiles and mark the active one |
| [`verify`](#verify) | Probe every profile against GitHub, concurrently |
| [`clean`](#clean) | Unset a repository's local identity |
| [`remove`](#remove) | Drop a profile, optionally deleting its key |
| [`rewrite`](#rewrite) | Repair the authorship of existing commits |

### `add`

```console
$ git-persona add
Profile name: personal
Git email: me@example.com

Profile "personal" created.
Key: /home/user/.ssh/id_ed25519_personal

Add this public key to your Git provider:

ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI... me@example.com

Then run: git-persona use personal
```

Non-interactive: `git-persona add --name work --email dev@acme-corp.com`.

`add` only registers the profile — it does not switch to it.

> **Tip.** The profile name becomes your `user.name` on every commit. Name
> profiles after the identity you want in the history, not after the role.

**Recovering an existing key.** If a key already exists for that profile
name — left behind by a removed profile, or copied from another machine —
`add` shows it and asks instead of deciding for you:

```console
A key already exists at:
  /home/user/.ssh/id_ed25519_legacy

Its public key is:

ssh-ed25519 AAAAC3NzaC1lZDI1NTE5... old-address@previous-job.com

An SSH key for this profile already exists. Do you want to (R)ecover the existing key or (O)verwrite it with a new one? [R/o]:
```

The comment at the end of the public key tells you which account it belongs
to. `R` reuses it; `O` replaces it. An empty answer or a closed stdin takes the
**safe** path and recovers, so a scripted run can never silently destroy a key
that is already registered with a provider.

### `use`

```console
$ git-persona use work
Now using "work" (dev@acme-corp.com)
```

### `list`

```console
$ git-persona list
   NAME      EMAIL              KEY
*  work      dev@acme-corp.com  /home/user/.ssh/id_ed25519_work
   personal  me@example.com     /home/user/.ssh/id_ed25519_personal
```

### `verify`

Probes every registered profile against GitHub **concurrently**, so checking
ten identities takes about as long as the slowest one rather than the sum.

```console
$ git-persona verify
STATUS   PROFILE   ACCOUNT   TOOK
OK       work      acme-bot  412ms
DENIED   personal  -         389ms

Error: 1 of 2 profiles failed to authenticate
```

Exits non-zero when any profile fails, which makes it usable as a health check
in a script. `--timeout` bounds each probe (default 10s); `--concurrency` caps
simultaneous probes.

### `clean`

A repository with its own `user.email` in `.git/config` ignores whatever you
set globally. `clean` unsets the three local keys:

```console
$ git-persona clean                      # current directory
Cleared local identity in .

$ git-persona clean ~/code/old-project
Cleared local identity in /home/user/code/old-project
```

Idempotent — cleaning an already-clean repository succeeds.

### `remove`

```console
$ git-persona remove personal
Removed profile "personal".
Its SSH key was kept at: /home/user/.ssh/id_ed25519_personal
Re-run with --purge-key to delete it, or remove it yourself.
```

The key is **kept by default**, because it may already be registered with a
provider and deleting it cannot be undone. `--purge-key` deletes both halves.

Removing the *active* profile also clears the three keys from your global Git
config, so Git is never left pointing at an identity that no longer exists.

### `rewrite`

Repairs the authorship of commits made under the wrong persona.

```console
$ git-persona rewrite personal --from dev@acme-corp.com
Would rewrite 5 of 30 commits on main to "personal <me@example.com>"

COMMIT    SUBJECT                                     CURRENT AUTHOR
9d098abf  feat(cli): make add recover or overwrite    work <dev@acme-corp.com>
ae45ecd1  test(cli): add failing specs for recovery   work <dev@acme-corp.com>
...

Nothing has changed. Re-run with --apply to rewrite.
```

Selection: `--from` matches an author email, `--commit` names a specific
commit, `--range` limits the scope. With none of them, every commit in the
range is rewritten.

**It is a dry run unless `--apply` is passed.** Applying records the original
head under `refs/git-persona/backup/<timestamp>` and prints both the
`git push --force-with-lease` needed to publish and the `git reset --hard`
needed to undo.

> **Rewriting and pull requests do not mix.** A PR *adds* commits; a rewrite
> *replaces* them. Rewriting a branch that shares commits with its base and
> then merging duplicates the history, and the identity you wanted gone
> survives in the old copy. Rewrite the destination branch directly.

## Where things live

| Path | What |
|---|---|
| `~/.git-persona/profiles.json` | Profile store, `0600`, inside a `0700` directory |
| `~/.ssh/id_ed25519_<profile>` | One key pair per profile |
| Global `.gitconfig` | Where the active identity is applied |
| `refs/git-persona/backup/*` | Pre-rewrite history, local only |

## Design notes

```
cmd/git-persona/     entry point and composition root
internal/cli/        cobra commands
internal/config/     profile model and JSON store
internal/git/        global apply, local unset
internal/ssh/        ed25519 keygen, GitHub probe
internal/verify/     concurrent probe fan-out
internal/rewrite/    commit authorship rewriting
```

Every package that touches the outside world sits behind a small interface —
`git.Runner`, `ssh.Runner`, `verify.Prober`, `rewrite.Runner` — so the entire
suite runs without spawning `git`, calling `ssh-keygen`, or opening a socket.
`cmd/git-persona/main.go` is the only place the real binaries are bound.

### Concurrent verification

One goroutine per profile, collected through a buffered channel closed by a
`WaitGroup`:

```go
ch := make(chan indexed, len(profiles))   // buffered to the batch size

for i, profile := range profiles {
    wg.Add(1)
    go func(i int, profile config.Profile) {
        defer wg.Done()
        ch <- indexed{i, probeOne(ctx, p, profile, opts.Timeout)}
    }(i, profile)
}

go func() { wg.Wait(); close(ch) }()

for r := range ch { results[r.i] = r.result }
```

The channel is buffered to the number of profiles so no worker can block on
send — that is what guarantees every goroutine exits. Each result carries its
index, so output order stays stable even though probes finish out of order. An
optional token channel caps concurrency and is left `nil` when unlimited, so
the common path pays nothing for the feature.

### Authorship rewriting

`rewrite` uses Git plumbing rather than `filter-branch` (deprecated by Git
itself, needs a POSIX shell) or `filter-repo` (an external Python script, which
would stop `git-persona` being a self-contained binary).

Commits are read with `rev-list` and `show`, rebuilt with `commit-tree`, and
the branch is moved with the compare-and-swap form of `update-ref`. Every
commit in range is rebuilt and all parents remapped, because rewriting an
ancestor changes the hash of every descendant. Both dates survive, so changing
authorship never moves the timeline.

## Testing

Built test-first. Each feature landed as a failing spec commit followed by the
implementation commit, which the Git history shows directly.

```console
make test          # go test ./...
make test-race     # go test -race ./...
make test-cover    # coverage summary
make vet
```

137 unit tests, none of which spawn a process or open a socket, plus an
integration suite behind a build tag that exercises `rewrite` against a real
`git` binary in a throwaway repository:

```console
go test -tags=integration ./internal/rewrite/
```

| Package | Coverage |
|---|---|
| `internal/verify` | 95.1% |
| `internal/cli` | 89.6% |
| `internal/config` | 87.1% |
| `internal/ssh` | 80.7% |
| `internal/rewrite` | 71.3% |
| `internal/git` | 70.7% |

CI runs the suite on Linux, Windows and macOS. The matrix is not decoration:
the file-permission specs are Unix-only, and the CRLF trimming and
`core.sshCommand` quoting exist specifically for Windows. It caught a real
Windows-only defect on its first run.

## Notes and caveats

- **Keys are generated without a passphrase.** `core.sshCommand` has to
  authenticate unattended, so the key's protection is its file permissions.
  `~/.ssh` is created `0700` and the store `0600`.
- **Windows paths are normalised.** Git parses `core.sshCommand` with
  shell-like rules where `\` is an escape character, so key paths are written
  with forward slashes and quoted.
- **`ssh -T git@github.com` exits 1 on success**, because GitHub refuses shell
  access. `verify` therefore reads the transcript rather than the exit status —
  an implementation that trusts the exit code reports every working key as
  broken.
- **Overwriting a key is crash-safe.** `add` with `O` moves the old pair aside
  rather than deleting it, so a failing `ssh-keygen` leaves the original intact.

## Roadmap

- [x] `add`, `use`, `list`, `clean`, `remove`
- [x] `verify` — concurrent GitHub probing with goroutines and channels
- [x] `rewrite` — repair commit authorship through Git plumbing
- [x] Windows installer with `gitp` alias, and a GoReleaser release pipeline
- [ ] Publish to Scoop and Homebrew taps
- [ ] Support providers beyond GitHub in `verify`
- [ ] Warn when rewriting a branch that shares commits with another

## Contributing

Issues and pull requests are welcome. The suite must stay green on all three
platforms, and new behaviour arrives test-first: a failing spec commit, then
the implementation.

## License

Apache License 2.0 — see [LICENSE](LICENSE) and [NOTICE](NOTICE).
