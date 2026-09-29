# git-persona

[![ci](https://github.com/Dkavila/git-persona/actions/workflows/ci.yml/badge.svg)](https://github.com/Dkavila/git-persona/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/Dkavila/git-persona.svg)](https://pkg.go.dev/github.com/Dkavila/git-persona)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

A small CLI for juggling multiple Git identities — work, personal, client — without ever editing `~/.ssh/config`.

```console
$ git-persona use work
Now using "work" (dev@acme-corp.com)
```

## The problem

You have a work GitHub account and a personal one. Both authenticate over SSH. The usual fix is a hand-maintained `~/.ssh/config` full of `Host github-work` aliases, which forces you to rewrite every remote URL to `git@github-work:org/repo.git`. Clone something with the normal URL and you silently push as the wrong person.

## The approach

`git-persona` never touches `~/.ssh/config` and never rewrites a remote. It writes three keys into your **global** Git config:

```ini
[user]
    name = work
    email = dev@acme-corp.com
[core]
    sshCommand = ssh -i "/home/user/.ssh/id_ed25519_work" -o IdentitiesOnly=yes
```

`core.sshCommand` tells Git which SSH binary and which key to use for every transport operation. `IdentitiesOnly=yes` stops the agent from offering other keys first, which is the usual cause of "you're authenticating as the wrong account". Remotes stay as `git@github.com:org/repo.git`.

Each profile gets its own ed25519 key pair, generated on `add`.

## Install

**Windows** — download `GitPersona_Installer.exe` from the [latest release](https://github.com/Dkavila/git-persona/releases/latest). It installs per-user (no administrator prompt), adds itself to your `PATH`, and provides the short alias `gitp`.

**Go toolchain** — any platform:

```console
go install github.com/Dkavila/git-persona/cmd/git-persona@latest
```

**From source:**

```console
git clone https://github.com/Dkavila/git-persona.git
cd git-persona
make build        # or: go build -o git-persona ./cmd/git-persona
```

Requires Go 1.27+, plus `git` and `ssh-keygen` on your `PATH`.

## Usage

### `add` — register a profile and generate its key

```console
$ git-persona add
Profile name: work
Git email: dev@acme-corp.com

Profile "work" created.
Key: /home/user/.ssh/id_ed25519_work

Add this public key to your Git provider:

ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI... dev@acme-corp.com

Then run: git-persona use work
```

Non-interactive, for scripts:

```console
$ git-persona add --name personal --email me@example.com
```

Paste the printed public key into your provider's SSH keys page. `add` only registers the profile — it does not switch to it.

**Recovering an existing key.** If a key already exists for that profile name — left behind by a removed profile, or copied from another machine — `add` shows you the key and asks what to do instead of deciding for you:

```console
$ git-persona add --name legacy --email dev@acme-corp.com

A key already exists at:
  /home/user/.ssh/id_ed25519_legacy

Its public key is:

ssh-ed25519 AAAAC3NzaC1lZDI1NTE5... old-address@previous-job.com

An SSH key for this profile already exists. Do you want to (R)ecover the existing key or (O)verwrite it with a new one? [R/o]:
```

The comment at the end of the public key tells you which account it belongs to. `R` reuses it; `O` replaces it. An empty answer or a closed stdin takes the **safe** path and recovers, so a scripted run can never silently destroy a key that is already registered with a provider.

### `use` — switch the global identity

```console
$ git-persona use work
Now using "work" (dev@acme-corp.com)
```

### `list` — see what you have

```console
$ git-persona list
   NAME      EMAIL              KEY
*  work      dev@acme-corp.com  /home/user/.ssh/id_ed25519_work
   personal  me@example.com     /home/user/.ssh/id_ed25519_personal
```

The `*` marks the active profile.

### `verify` — check every key at once

Probes every registered profile against GitHub **concurrently**, so checking ten identities takes about as long as the slowest one rather than the sum of all of them.

```console
$ git-persona verify
STATUS   PROFILE   ACCOUNT   TOOK
OK       work      acme-bot  412ms
DENIED   personal  -         389ms
OK       client    acme-ops  401ms

Error: 1 of 3 profiles failed to authenticate
```

Exits non-zero when any profile fails, which makes it usable as a health check in a script. `--timeout` bounds each probe (default 10s) and `--concurrency` caps simultaneous probes (default: one per profile).

### `clean` — let the global profile win again

A repository with its own `user.email` in `.git/config` ignores whatever you set globally. `clean` unsets the three local keys:

```console
$ git-persona clean            # current directory
Cleared local identity in .

$ git-persona clean ~/code/old-project
Cleared local identity in /home/user/code/old-project
```

It is idempotent — cleaning an already-clean repository succeeds.

### `remove` — drop a profile

```console
$ git-persona remove personal
Removed profile "personal".
Its SSH key was kept at: /home/user/.ssh/id_ed25519_personal
Re-run with --purge-key to delete it, or remove it yourself.
```

The key is **kept by default**, because it may already be registered with a provider and deleting it cannot be undone. `--purge-key` deletes both halves of the pair.

Removing the *active* profile also clears the three keys from your global Git config, so Git is never left pointing at an identity that no longer exists:

```console
$ git-persona remove work --purge-key
Removed profile "work" and deleted its key pair.

Warning: "work" was the active profile. user.name, user.email and
core.sshCommand have been unset from your global Git config.
Run "git-persona use <profile>" to select another identity.
```

## Where things live

| Path | What |
|---|---|
| `~/.git-persona/profiles.json` | Profile store, `0600`, inside a `0700` directory |
| `~/.ssh/id_ed25519_<profile>` | One key pair per profile |
| Global `.gitconfig` | Where the active identity is actually applied |

## Project layout

```
cmd/git-persona/     entry point and composition root
internal/cli/        cobra commands
internal/config/     profile model and JSON store
internal/git/        global apply, local unset
internal/ssh/        ed25519 keygen, GitHub probe
internal/verify/     concurrent probe fan-out
```

Every package that touches the outside world sits behind a small interface — `git.Runner`, `ssh.Runner`, `verify.Prober` — so the entire suite runs without spawning `git`, calling `ssh-keygen`, or opening a socket. `cmd/git-persona/main.go` is the only place the real binaries are bound.

### How `verify` fans out

One goroutine per profile, collected through a buffered channel closed by a `WaitGroup`:

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

The channel is buffered to the number of profiles so no worker can block on send — that is what guarantees every goroutine exits. Each result carries its index, so output order stays stable even though probes finish out of order. An optional token channel caps concurrency, and is left `nil` when unlimited so the common path pays nothing for the feature.

## Development

Built test-first. Each feature landed as a failing spec commit followed by the implementation commit, which the Git history shows directly.

```console
make test          # go test ./...
make test-race     # go test -race ./...
make test-cover    # coverage summary
make vet
```

| Package | Coverage |
|---|---|
| `internal/verify` | 95.1% |
| `internal/cli` | 88.3% |
| `internal/config` | 87.1% |
| `internal/ssh` | 80.7% |
| `internal/git` | 70.7% |

CI runs the suite on Linux, Windows and macOS. The matrix is not decoration: the file-permission specs are Unix-only, and the CRLF trimming and `core.sshCommand` quoting exist specifically for Windows.

## Notes and caveats

- **Keys are generated without a passphrase.** `core.sshCommand` has to authenticate unattended, so the key's protection is its file permissions. `~/.ssh` is created `0700` and the store `0600`.
- **Windows paths are normalised.** Git parses `core.sshCommand` with shell-like rules where `\` is an escape character, so key paths are written with forward slashes and quoted. `C:/Users/Jane Doe/.ssh/...` works.
- **`ssh -T git@github.com` exits 1 on success**, because GitHub refuses shell access. `verify` therefore reads the transcript rather than the exit status — an implementation that trusts the exit code reports every working key as broken.
- **Overwriting a key is crash-safe.** `add` with `O` moves the old pair aside rather than deleting it, so a failing `ssh-keygen` leaves the original intact instead of destroying a key that may be registered with a provider. The backup is discarded only once the new key is in place.

## Roadmap

- [x] `add`, `use`, `list`, `clean`, `remove`
- [x] `verify` — concurrent GitHub probing with goroutines and channels
- [x] Windows installer with `gitp` alias, and a GoReleaser release pipeline
- [ ] Publish to Scoop and Homebrew taps
- [ ] Support providers beyond GitHub in `verify`

## License

Apache License 2.0 — see [LICENSE](LICENSE) and [NOTICE](NOTICE).
