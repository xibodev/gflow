# gflow

Lean, extensible multi-provider CLI, MCP server, and protocol shell for AI media generation (images, video, audio, and chat).

`gflow` is an open-source shell designed around an out-of-process provider adapter architecture. The core binary contains zero proprietary or reverse-engineered code; all provider backends (Gemini, Flow, MiniMax, custom models) run as decoupled adapter plugins communicating over standard JSON-RPC.

## Architecture

```
┌────────────────────────────────────────────────────────┐
│                      gflow CLI                         │
│  (image, video, audio, chat, status, login, history)   │
├────────────────────────────────────────────────────────┤
│                     gflow MCP                          │
│     (Claude Desktop, Cursor, OpenCode, Windsurf)       │
├──────────────────────────┬─────────────────────────────┤
│     pkg/application      │         pkg/adapter         │
│  (capability contracts)  │  (out-of-process protocol)  │
└────────────┬─────────────┴──────────────┬──────────────┘
             │                            │
             │ JSON-RPC 2.0 (stdio)       │ JSON-RPC 2.0 (stdio)
             ▼                            ▼
  ┌──────────────────────┐     ┌──────────────────────┐
  │   gflow-adapter      │     │  custom-adapter      │
  │ (Gemini, Flow, ...)  │     │   (Flux, ComfyUI)    │
  └──────────────────────┘     └──────────────────────┘
```

## Quick Start

### Installation

```bash
go install github.com/xibodev/gflow/cmd/gflow@latest
```

### Installing Provider Adapters

Install official or third-party adapters by placing their executables in `~/.gflow/adapters/` or on your `PATH`:

```bash
# Example: placing an adapter in the standard directory
mkdir -p ~/.gflow/adapters
cp gflow-adapter ~/.gflow/adapters/
```

Check adapter discovery and status:

```bash
gflow status
```

### Commands

```bash
# Generate images
gflow image "a cybernetic landscape at sunset" -P gemini

# Generate videos
gflow video "a golden retriever running on the beach" -P flow

# Generate music tracks
gflow audio "calm piano and acoustic guitar" -P gemini

# Chat
gflow chat "explain quantum computing in one sentence" -P gemini -m pro

# View generated history
gflow history --limit 10

# Configure MCP for AI coding tools
gflow mcp setup
```

## Writing Adapters

Any executable in any language (Go, Python, Rust, Node) can serve as a `gflow` adapter by implementing the JSON-RPC protocol over stdin/stdout.

See the complete [Adapter Protocol Specification](docs/adapter-protocol.md).

## License

MIT
