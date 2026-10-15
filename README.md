# Parallels Desktop MCP

[![Join the pdfm-mcp community on Discord — join the server, then head to #pdfm-mcp](docs/assets/discord-banner.svg)](https://discord.gg/7y3AxDUc8Y)

Connect your AI assistant to Parallels Desktop. **pdfm-mcp** lets Claude, Codex, and other MCP clients find and manage your virtual machines, run guest commands, read the screen, and automate keyboard input.

Runs locally on macOS, with a universal binary for Apple Silicon and Intel. No Go or Node installation is needed to use the released binary.

## What you can do

- Find a VM, check its status, and start or stop it.
- Run commands inside Windows or Linux guests and collect their output.
- Take screenshots, read screen text, and type into the guest.
- Create snapshots, clone VMs, and automate multi-step workflows.

Once connected, try asking your assistant: “Find my Ubuntu VM and show its status” or “Take a screenshot of my running Windows VM.”

## Install

You need **macOS**, **Parallels Desktop**, and an MCP client. Guest command execution also requires **Parallels Tools** in the guest. Screen capture and keyboard tools need a running VM.

Homebrew and the default installer use stable releases. Until the first stable release and cask are published, use the script's prerelease option below. Check the [Releases page](https://github.com/Parallels/pdfm-mcp/releases) for available builds.

### Homebrew

```sh
brew tap Parallels/tap
brew install --cask Parallels/tap/pdfm-mcp
pdfm-mcp --version
```

### Install script

```sh
curl -fsSL https://raw.githubusercontent.com/Parallels/pdfm-mcp/main/scripts/install.sh | bash
/usr/local/bin/pdfm-mcp --version
```

The script verifies the release archive's SHA256 checksum and installs to `/usr/local/bin`. It asks for sudo only when the destination requires it.

To install the latest canary or beta:

```sh
curl -fsSL https://raw.githubusercontent.com/Parallels/pdfm-mcp/main/scripts/install.sh | bash -s -- --pre-release
```

For a particular version or a different directory, add `--version <version>` or `--path <directory>` after `bash -s --`.

### Claude Desktop bundle

If the release includes `pdfm-mcp-<version>.mcpb`, download it and its matching `.sha256` file from [Releases](https://github.com/Parallels/pdfm-mcp/releases). Verify it with `shasum -a 256 -c pdfm-mcp-<version>.mcpb.sha256`, then double-click the bundle to install. This is an alternative to configuring the binary manually below.

## Connect your assistant

Your client starts the server for you. Use the binary's **absolute path**, since desktop apps may not inherit your terminal's PATH.

| Installation | Binary path |
| --- | --- |
| Homebrew, Apple Silicon | `/opt/homebrew/bin/pdfm-mcp` |
| Homebrew, Intel | `/usr/local/bin/pdfm-mcp` |
| Install script, default directory | `/usr/local/bin/pdfm-mcp` |
| Install script, custom directory | `<directory>/pdfm-mcp` |

For Homebrew, `"$(brew --prefix)/bin/pdfm-mcp"` resolves the right path in terminal commands. In JSON or TOML configuration, use the actual path from the table; `$(brew --prefix)` is not expanded there.

### Claude Code

Choose the command matching your installation:

```sh
# Homebrew (Apple Silicon or Intel)
claude mcp add --transport stdio --scope user pdfm-mcp -- "$(brew --prefix)/bin/pdfm-mcp"

# Install script
claude mcp add --transport stdio --scope user pdfm-mcp -- /usr/local/bin/pdfm-mcp
```

Run `claude mcp list` to check the connection. These commands make the server available across your projects. See [Claude Code's MCP setup](https://code.claude.com/docs/en/mcp) for more options.

### Codex CLI and IDE extension

Choose the command matching your installation:

```sh
# Homebrew (Apple Silicon or Intel)
codex mcp add pdfm-mcp -- "$(brew --prefix)/bin/pdfm-mcp"

# Install script
codex mcp add pdfm-mcp -- /usr/local/bin/pdfm-mcp
```

Run `codex mcp list` to confirm registration, then use `/mcp` in the CLI to check active servers. The CLI and IDE extension share the same MCP configuration.

If you prefer editing `~/.codex/config.toml`, add:

```toml
[mcp_servers.pdfm-mcp]
command = "/opt/homebrew/bin/pdfm-mcp"
```

For the script installation or Intel Homebrew, change `command` to `/usr/local/bin/pdfm-mcp`. See the [official Codex MCP documentation](https://developers.openai.com/codex/mcp/).

#### Plugin marketplaces

This repo is also a plugin marketplace for several assistants. Install the `pdfm-mcp` binary first and make sure it is on your `PATH`; the plugins only configure the client to launch it.

```sh
# Codex
codex plugin marketplace add Parallels/pdfm-mcp
codex plugin add pdfm-mcp@parallels

# Claude Code
claude plugin marketplace add Parallels/pdfm-mcp
claude plugin install pdfm-mcp@parallels

# Gemini CLI
gemini extensions install https://github.com/Parallels/pdfm-mcp
```

Cursor Teams and Enterprise admins can import this repository from **Dashboard → Plugins & MCPs → Team Marketplaces**.

### Claude Desktop

Open `~/Library/Application Support/Claude/claude_desktop_config.json` and add the server to `mcpServers`, preserving any existing entries:

```json
{
  "mcpServers": {
    "pdfm-mcp": {
      "command": "/opt/homebrew/bin/pdfm-mcp"
    }
  }
}
```

This example uses Homebrew on Apple Silicon. For the script installation or Intel Homebrew, change `command` to `/usr/local/bin/pdfm-mcp`. Restart Claude Desktop after saving.

### VS Code / GitHub Copilot

Add the server to `.vscode/mcp.json` in your project, preserving any existing servers:

```json
{
  "servers": {
    "pdfm-mcp": {
      "type": "stdio",
      "command": "/opt/homebrew/bin/pdfm-mcp"
    }
  }
}
```

For the script installation or Intel Homebrew, change `command` to `/usr/local/bin/pdfm-mcp`. Run **MCP: List Servers** from the Command Palette to start the server and check its status. See [VS Code's MCP setup](https://code.visualstudio.com/docs/agents/reference/mcp-configuration).

## Available tools

By default, your assistant can work with all local VMs. Tools that target a VM accept its UUID or exact name. To restrict the server to one VM, add `--vm-id "My VM"` to its launch arguments.

| Tool | What it does |
| --- | --- |
| `parallels_list_vms` | List your VMs and their details. |
| `parallels_search_vms` | Find VMs by part of their name or UUID, optionally filtering by state. |
| `parallels_vm_info` | Get detailed information about one VM. |
| `parallels_vm_start`, `parallels_vm_resume` | Start or resume a VM. |
| `parallels_vm_pause`, `parallels_vm_suspend` | Pause execution or save the VM's running state. |
| `parallels_vm_stop`, `parallels_vm_restart` | Stop or gracefully restart a VM. |
| `parallels_vm_reset`, `parallels_vm_reset_uptime` | Hard-reset a VM or reset its uptime counter. |
| `parallels_vm_capture` | Take a screenshot of the VM. |
| `parallels_vm_observe` | Read screen text, find text, or report changes using local OCR. |
| `parallels_vm_exec` | Run a guest command and return its exit code and output. |
| `parallels_vm_exec_start`, `parallels_vm_exec_result`, `parallels_vm_exec_cancel` | Start, monitor, or cancel a background command job. |
| `parallels_vm_type`, `parallels_vm_send_key` | Type text or send keys and shortcuts to the guest. |
| `parallels_vm_sequence` | Run a workflow of tool calls, waits, and checks in one request. |
| `parallels_vm_snapshot_create`, `parallels_vm_snapshot_list` | Create checkpoints and browse a VM's snapshots. |
| `parallels_vm_snapshot_switch`, `parallels_vm_snapshot_delete` | Restore a checkpoint or delete a snapshot. |
| `parallels_vm_clone` | Create a copy of a VM. |
| `parallels_vm_set` | Change a VM's configuration (CPU, memory, boot, sharing, security and more — see the tool's own field descriptions). |
| `parallels_vm_device_add` | Add a device (hard drive, optical drive, network adapter, serial/parallel port, sound card, USB support). |
| `parallels_vm_device_set` | Modify, enable/disable or hot connect/disconnect an existing device. |
| `parallels_vm_device_del` | Remove a device; optionally destroy a virtual hard-drive image (confirmation-gated). |
| `parallels_vm_delete` | Permanently delete a VM and its files; requires explicit confirmation. |
| `parallels_vm_reclaim_disk` | Estimate or reclaim unused VM disk space. |
| `parallels_vm_encrypt`, `parallels_vm_decrypt`, `parallels_vm_change_encryption_password` | Manage VM encryption and its password. |

See the [tool reference](docs/tools.md) for arguments, limits, and automation examples.

## Choose what your assistant can access

Add launch arguments to limit access:

- `--read-only`: expose observation tools for inspecting VMs and their screens.
- `--vm-id "My VM"`: target one VM.
- `--allow-vms "My VM,Another VM"`: restrict access to selected VMs.
- `--allow-tools` / `--deny-tools`: expose or hide specific tools.

For example, add `"args": ["--read-only", "--vm-id", "My VM"]` beside `command` in a JSON client configuration. In Codex TOML, use `args = ["--read-only", "--vm-id", "My VM"]`.

Screenshots, recognized text, command output, and tool arguments reach your MCP client and its model provider. VM deletion, snapshot rollback, guest commands, and encryption can change or destroy data; review those actions before approving them.

Release builds can collect anonymised tool usage telemetry. To opt out, set `PDFM_MCP_TELEMETRY_DISABLED=true` in your server's environment. See [configuration and privacy](docs/configuration.md) for all settings.

## Update or uninstall

With Homebrew:

```sh
brew upgrade --cask pdfm-mcp
brew uninstall --cask pdfm-mcp
```

With the install script:

```sh
# Update to the latest stable release
curl -fsSL https://raw.githubusercontent.com/Parallels/pdfm-mcp/main/scripts/install.sh | bash -s -- --update

# Uninstall
curl -fsSL https://raw.githubusercontent.com/Parallels/pdfm-mcp/main/scripts/install.sh | bash -s -- --uninstall
```

Add `--pre-release` when updating a canary or beta installation, or `--path <directory>` if you installed to a custom directory. Remove the server from your client's MCP settings after uninstalling.

## Help and contributing

Join [Discord](https://discord.gg/7y3AxDUc8Y) and open **#pdfm-mcp**, or [report an issue](https://github.com/Parallels/pdfm-mcp/issues).

To build, test, debug, or add tools, see [CONTRIBUTING.md](CONTRIBUTING.md).
