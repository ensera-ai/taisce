<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Set up your machine

Taisce runs in containers and reads conversations with a hosted model. So every machine needs the
same things, and differs only in how you get them:

1. **Docker with the Compose plugin**, so `docker compose version` answers.
2. **`jq`** to read JSON, and **`uuidgen`** to make idempotency keys.
3. **A way out to the model's host.** Taisce's worker calls `api.deepseek.com` over HTTPS from inside
   a container.

Follow the section for your system, then run [the check](#check-that-a-container-reaches-the-model).
It is the same on every system.

## Linux

### Docker

Install Docker Engine and the Compose plugin from Docker's own repository, following
[Install Docker Engine](https://docs.docker.com/engine/install/) for your distribution. Every
distribution ends with the same packages:

```bash
sudo apt-get install docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin   # Ubuntu, Debian
sudo dnf install docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin       # Fedora, RHEL
```

On Fedora and RHEL, installing Docker does not start it. Enable it, so it also starts after a reboot:

```bash
sudo systemctl enable --now docker   # Fedora, RHEL
```

Ubuntu and Debian start Docker when the package is installed.

To run `docker` without `sudo`, add yourself to the `docker` group and log in again. Membership of
that group is equivalent to root on the machine, so decide it rather than copy it:

```bash
sudo usermod -aG docker "$USER"
```

### jq and uuidgen

```bash
sudo apt-get update && sudo apt-get install jq uuid-runtime   # Ubuntu, Debian
sudo dnf install jq util-linux                                # Fedora, RHEL
```

A fresh Ubuntu or Debian has no package lists, so `apt-get install` alone answers "Unable to locate
package" until `apt-get update` has run.

## macOS with Colima

Colima runs Docker in a small Linux virtual machine. The images are published for `linux/arm64`, so
on Apple silicon they run without emulation. `uuidgen` comes with macOS.

```bash
brew install colima docker docker-compose jq
```

Homebrew installs Compose as a plugin in a directory Docker doesn't search by default. Add it to
`~/.docker/config.json`, next to anything already in that file:

```json
{
  "cliPluginsExtraDirs": ["/opt/homebrew/lib/docker/cli-plugins"]
}
```

`/opt/homebrew` is the prefix on Apple silicon; `brew --prefix` prints yours.

Start the virtual machine. It holds PostgreSQL and Taisce, and the model runs at the provider:

```bash
colima start --cpu 4 --memory 8
```

Using Docker Desktop instead of Colima? Skip the Colima steps and run the check. Nothing else on this
page changes.

## Windows with WSL2

Every command in the quickstart is a Linux shell command. On Windows, run them in a Linux distribution
under WSL2, not in PowerShell. WSL needs Windows 10 version 2004 (build 19041) or later, or Windows 11.

In PowerShell, as administrator:

```powershell
wsl --install
```

Restart when asked. This installs Ubuntu. Open **Ubuntu** from the Start menu; the first time, it asks
you to create a Linux user name and password. Work in your Linux home directory (`cd ~`), not under
`/mnt/c`: Linux reaches files on the Windows drive much more slowly. Inside Ubuntu:

```bash
sudo apt-get update && sudo apt-get install jq uuid-runtime
```

Then choose where Docker runs. Use one or the other: Docker Desktop requires that Docker Engine is
not also installed inside the distribution.

- **With Docker Desktop.** Its WSL 2 backend needs WSL 2.1.5 or later, so run `wsl --update` in
  PowerShell first. Install [Docker Desktop](https://docs.docker.com/desktop/setup/install/windows-install/)
  with the WSL 2 based engine. Under **Settings → Resources → WSL integration**, check that Ubuntu is
  on. `docker` inside Ubuntu then talks to Docker Desktop.
- **With Docker Engine inside Ubuntu.** Follow the [Linux](#linux) section inside Ubuntu.

## Check that a container reaches the model

```bash
docker compose version
docker run --rm curlimages/curl:8.11.1 -sS -o /dev/null -w '%{http_code}\n' --max-time 10 https://api.deepseek.com/models
```

You should see the Compose version, then `401`:

```text
Docker Compose version …
401
```

`401` is DeepSeek refusing a request that carries no key, which proves a container reached it over
HTTPS. Taisce's worker makes the same call with your key.

### When the check fails

| What you see | Most likely | Fix |
|---|---|---|
| `curl: (6) Could not resolve host` | Containers can't resolve names. | Check the host itself resolves `api.deepseek.com`, then Docker's DNS setting. |
| `curl: (7) Failed to connect` or `curl: (28)` after 10 seconds | A firewall or proxy blocks outbound HTTPS from containers. | Allow outbound port 443 to `api.deepseek.com`, or configure Docker for your proxy. |
| `curl: (60) SSL certificate problem` | A proxy inspects TLS with its own certificate. | Ask for the proxy's certificate authority, or an exception for `api.deepseek.com`. |
| `failed to connect to the docker API at unix:///var/run/docker.sock` | Docker is installed but not running. This is the default on Fedora and RHEL. | `sudo systemctl enable --now docker`, then run the check again. |

`docker: command not found` or `'compose' is not a docker command` means Docker or the Compose
plugin is missing: go back to your system's Docker steps.

## What this page was run on

The macOS steps were run on 2026-09-13 on Apple silicon, with Colima 0.10.3 and Docker Compose 5.5.0,
and the check printed `401`.

The Linux steps were run on 2026-09-14 on fresh virtual machines. Each machine ran Docker's own
repository setup, the commands above and the check, which printed `401`, then the quickstart end to end.
- **Ubuntu 26.04 LTS:** Docker 29.8.0 and Docker Compose 5.5.1.
- **Fedora 44:** the same versions. On Fedora the check first failed because Docker was not running,
  which is why the page now enables it.

Both machines were arm64. The amd64 packages and images were not run on Linux.

The Windows section follows Docker's and Microsoft's documentation as read on 2026-09-14, and has not
been run on Windows yet. [#84](https://github.com/ensera-ai/taisce/issues/84) tracks running it.
