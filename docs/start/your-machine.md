<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Set up your machine

Taisce runs in containers. So every machine needs the same things, and differs only in how you get
them:

1. **Docker with the Compose plugin**, so `docker compose version` answers.
2. **`jq`** to read JSON, and **`uuidgen`** to make idempotency keys.
3. **Ollama, reachable from inside a container** at `http://host.docker.internal:11434`, **only if you
   run the model on your own machine.** The compose file maps that name to your machine, and Taisce's
   worker calls the model through it. With DeepSeek's hosted API, which the
   [quickstart](../developers/quickstart.md) prefers, skip the Ollama steps and the check below.

Follow the section for your system, then run [the check](#check-that-a-container-reaches-the-model).
It is the same on every system, and it tests the one thing that differs between them: whether a
container can reach the model.

## Linux

### Docker

Install Docker Engine and the Compose plugin from Docker's own repository, following
[Install Docker Engine](https://docs.docker.com/engine/install/) for your distribution. Every
distribution ends with the same packages:

```bash
sudo apt-get install docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin   # Ubuntu, Debian
sudo dnf install docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin       # Fedora, RHEL
```

To run `docker` without `sudo`, add yourself to the `docker` group and log in again. Membership of
that group is equivalent to root on the machine, so decide it rather than copy it:

```bash
sudo usermod -aG docker "$USER"
```

### Ollama

```bash
curl -fsSL https://ollama.com/install.sh | sh
```

The installer runs Ollama as a systemd service listening on `127.0.0.1:11434`. On Linux a container
cannot reach that address: there, `host.docker.internal` is the address of Docker's bridge, not your
machine's loopback. Tell the service to listen on every interface:

```bash
sudo systemctl edit ollama.service
```

In the editor that opens, add:

```ini
[Service]
Environment="OLLAMA_HOST=0.0.0.0:11434"
```

Then restart it:

```bash
sudo systemctl daemon-reload
sudo systemctl restart ollama
```

Don't also start `ollama serve` in a terminal: the service already holds the port.

**Ollama has no authentication.** Listening on `0.0.0.0` lets anything that can reach your machine
use it, not only your containers. On a machine other people can reach, allow port 11434 only from
Docker's networks, which Docker takes from `172.17.0.0/16` upwards by default. With ufw, which denies
incoming connections by default:

```bash
sudo ufw allow from 172.16.0.0/12 to any port 11434 proto tcp
```

### jq and uuidgen

```bash
sudo apt-get install jq uuid-runtime   # Ubuntu, Debian
sudo dnf install jq util-linux          # Fedora, RHEL
```

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

Start the virtual machine. The model doesn't run inside it, so it only needs room for PostgreSQL and
Taisce:

```bash
colima start --cpu 4 --memory 8
```

Install Ollama from [ollama.com/download](https://ollama.com/download) and open it. It listens on
`127.0.0.1:11434`, and under Colima `host.docker.internal` reaches your Mac's loopback, so containers
reach Ollama as it is. **Leave `OLLAMA_HOST` unset.** That keeps a server with no authentication off
your network.

Using Docker Desktop instead of Colima? Skip the Colima steps and run the check. Nothing else on this
page changes.

## Windows with WSL2

Every command in the quickstart is a Linux shell command. On Windows, run them in a Linux distribution
under WSL2, not in PowerShell.

In PowerShell, as administrator:

```powershell
wsl --install
```

Restart when asked. This installs Ubuntu. Open **Ubuntu** from the Start menu and work in your Linux
home directory (`cd ~`), not under `/mnt/c`: Linux reaches files on the Windows drive much more
slowly. Inside Ubuntu:

```bash
sudo apt-get install jq uuid-runtime
```

Then choose where Docker runs. Use one or the other: Docker Desktop requires that Docker Engine is
not also installed inside the distribution.

### With Docker Desktop

1. Install [Docker Desktop](https://docs.docker.com/desktop/setup/install/windows-install/) with the
   WSL 2 based engine. Under **Settings → Resources → WSL integration**, check that Ubuntu is on.
   `docker` inside Ubuntu then talks to Docker Desktop.
2. Install [Ollama for Windows](https://ollama.com/download/windows).
3. Run [the check](#check-that-a-container-reaches-the-model) from Ubuntu. If it can't reach Ollama,
   make Ollama listen beyond loopback: in PowerShell, run the line below, then quit Ollama from the
   taskbar and start it again from the Start menu. If Windows Firewall asks, allow Ollama on private
   networks only.

   ```powershell
   [Environment]::SetEnvironmentVariable("OLLAMA_HOST", "0.0.0.0:11434", "User")
   ```

### With Docker Engine inside Ubuntu

Follow the [Linux](#linux) section inside Ubuntu, for Docker and for Ollama. Run Ollama inside Ubuntu
too, not on Windows: with Docker Engine in the distribution, `host.docker.internal` is the WSL
virtual machine, which is where Ollama then is.

- **With an NVIDIA GPU,** install the driver on Windows only, and no Linux display driver inside WSL.
  WSL passes the GPU through to Ubuntu.
- **If `systemctl` isn't available** in your distribution, the installer can't run Ollama as a
  service. Run `OLLAMA_HOST=0.0.0.0:11434 ollama serve` in a terminal of its own instead.

## Check that a container reaches the model

```bash
docker compose version
docker run --rm --add-host host.docker.internal:host-gateway curlimages/curl:8.11.1 \
  -sS --max-time 5 http://host.docker.internal:11434/v1/models | jq -r '.data[].id'
```

You should see the Compose version, then every model Ollama has. Before you pull one, the list is
empty and the command prints nothing after the version, but it doesn't fail either:

```text
Docker Compose version …
qwen3.6:35b-a3b-q8_0
```

The model's tag depends on the platform: the quickstart's
[step 1](../developers/quickstart.md#ollama-everything-stays-on-your-machine) names the build for
Apple silicon and the one for Linux and Windows.

The second command starts a throwaway container and asks Ollama for its models from inside it. That
is the path Taisce's worker takes; `--add-host` does for this container what the compose file does
for Taisce's.

### When the check fails

`curl: (7) Failed to connect to host.docker.internal port 11434` means nothing answered from inside
the container:

| System | Most likely | Fix |
|---|---|---|
| Linux | Ollama listens on `127.0.0.1` only. `ss -ltn \| grep 11434` shows it. | Set `OLLAMA_HOST` in the service, as above. |
| Linux | A firewall drops connections from Docker's networks. | Allow port 11434 from them, as above. |
| macOS with Colima | Ollama isn't running. `curl -s localhost:11434/v1/models` on the Mac fails too. | Open the Ollama app. |
| Windows, Docker Desktop | Ollama listens on loopback only, or the firewall blocks it. | Set `OLLAMA_HOST`, as above. |
| Windows, Docker Engine in Ubuntu | Ollama runs on Windows, outside the distribution. | Install and run it inside Ubuntu. |

`docker: command not found` or `'compose' is not a docker command` means Docker or the Compose
plugin is missing: go back to your system's Docker steps.

## What this page was run on

The macOS steps were run on 2026-09-13 on Apple silicon, with Colima 0.10.3 (`vz`, with a network
address) and Docker Compose 5.5.0. A container reached a server bound only to the Mac's `127.0.0.1`
through `host.docker.internal`, and the check listed Ollama's models.

The Linux and Windows sections follow Docker's, Ollama's and Microsoft's documentation as read on that
day, and have not been run on those systems yet.
[#33](https://github.com/ensera-ai/taisce/issues/33) tracks running them.
