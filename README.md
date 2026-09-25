# git-persona

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

```console
go install github.com/Dkavila/git-persona/cmd/git-persona@latest
```

Or from source:

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

### `clean` — let the global profile win again

A repository with its own `user.email` in `.git/config` ignores whatever you set globally. `clean` unsets the three local keys:

```console
$ git-persona clean            # current directory
Cleared local identity in .

$ git-persona clean ~/code/old-project
Cleared local identity in /home/user/code/old-project
```

It is idempotent — cleaning an already-clean repository succeeds.

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
```

Every package that touches the outside world sits behind a small interface — `git.Runner`, `ssh.Runner` — so the entire suite runs without spawning `git`, calling `ssh-keygen`, or opening a socket. `cmd/git-persona/main.go` is the only place the real binaries are bound.

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
| `internal/git` | 92.1% |
| `internal/config` | 87.1% |
| `internal/cli` | 85.4% |
| `internal/ssh` | 80.0% |

## Notes and caveats

- **Keys are generated without a passphrase.** `core.sshCommand` has to authenticate unattended, so the key's protection is its file permissions. `~/.ssh` is created `0700` and the store `0600`.
- **Windows paths are normalised.** Git parses `core.sshCommand` with shell-like rules where `\` is an escape character, so key paths are written with forward slashes and quoted. `C:/Users/Jane Doe/.ssh/...` works.
- **`ssh -T git@github.com` exits 1 on success**, because GitHub refuses shell access. Any connectivity check here reads the transcript rather than the exit status.

## Roadmap

- [x] `add`, `use`, `list`, `clean`
- [ ] `verify` — concurrently probe every registered profile against GitHub using goroutines and channels, so checking ten identities takes as long as the slowest one rather than the sum
- [ ] `--version` build metadata and CI

## License

Not yet chosen. Add a `LICENSE` file before sharing this publicly.
