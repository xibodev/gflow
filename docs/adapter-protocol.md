# Private Adapter Protocol

The private adapter seam runs a caller-selected executable as a child process and
uses newline-delimited JSON-RPC 2.0 over stdin/stdout. It is deliberately smaller
than MCP and has its own gflow protocol version (`"1"`). It does not expose tools,
routes, dynamic commands, discovery, downloads, or shell execution.

## Trust Model

An adapter is trusted local code with the invoking user's permissions. gflow does
not sandbox it. The caller must supply an absolute executable path and explicit
arguments. Secrets must not be placed in arguments because process command lines
may be observable. The client environment is explicit and is not inherited;
adapter-specific secrets may be supplied there.

## User Configuration

Adapters are opt-in for `gflow image`, `gflow video`, and `gflow mcp` only.
Select a non-built-in provider with `--provider` or `GFLOW_PROVIDER`, then set:

- `GFLOW_ADAPTER_COMMAND`: required absolute executable path.
- `GFLOW_ADAPTER_ARGS_JSON`: optional JSON string array.
- `GFLOW_ADAPTER_ENV_JSON`: optional JSON object with string values. This is the
  complete child environment, not additions to the inherited environment.

Example with placeholders:

```bash
export GFLOW_PROVIDER="vendor.provider"
export GFLOW_ADAPTER_COMMAND="/absolute/path/to/gflow-provider-adapter"
export GFLOW_ADAPTER_ARGS_JSON='["--config","/absolute/path/to/config.json"]'
export GFLOW_ADAPTER_ENV_JSON='{"PROVIDER_TOKEN":"<secret>","REGION":"<region>"}'
gflow video "a paper boat crossing a moonlit lake"
```

Never put secrets in `GFLOW_ADAPTER_ARGS_JSON`; command lines may be observable.
There is no PATH scan, shell, auto-download, or implicit environment inheritance.
The initialized `provider_id` must exactly match the selected provider. Adapter
stdout is protocol-only and must never contain logs or banners; use stderr for
diagnostics. `gflow serve` and its HTTP API intentionally do not support external
adapters in protocol v1.

Stdout is reserved exclusively for one JSON-RPC message per line. Messages are
bounded (1 MiB by default). Diagnostic output belongs on stderr, which is inherited
by default or may be directed to a separate writer. Stderr is never parsed as
protocol traffic.

## Initialization

The first request is:

```json
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocol_version":"1","core_version":"<gflow version>"}}
```

The result must contain:

```json
{
  "provider_id": "vendor.provider",
  "display_name": "Provider Name",
  "adapter_version": "1.2.3",
  "protocol_version": "1",
  "capabilities": ["image-generation", "video-submit", "video-poll"]
}
```

Provider IDs are stable lowercase identifiers beginning with a letter, with
optional dot or hyphen-separated alphanumeric segments. Protocol mismatch,
unknown or duplicate capabilities, invalid identifiers, and missing descriptor
fields reject the adapter before registration.
`video-submit` and `video-poll` form one asynchronous-video contract and must be
declared together; a descriptor containing only one is rejected during initialization.
`auth-login` requires `auth-status`; a descriptor with `auth-login` alone is rejected.
Existing adapters without auth capabilities remain valid; protocol v1 is additive.

## Operations

Only declared capabilities may be called. Version 1 defines these methods:

| Capability | Method | Result |
| --- | --- | --- |
| `image-generation` | `image.generate` | `{"assets":[...]}` |
| `video-submit` | `video.submit` | `{"operation_id":"opaque"}` |
| `video-poll` | `video.poll` | operation ID, status, optional assets/error |
| `auth-status` | `auth.describe` | `{"ready":bool,"summary":"...","checks":[],"next_steps":[]}` |
| `auth-login` | `auth.login` | same shape as `auth.describe` after login automation |

Requests use normalized prompt, aspect, model, duration, resolution, seed, and
reference fields. Assets use gflow's normalized `id`, `type`, `url`, `local_path`,
`prompt`, dimensions, size, and MIME type fields. Image assets have type `image`;
video assets have type `video`. Video status is one of `queued`, `running`,
`succeeded`, or `failed`.

```json
{"jsonrpc":"2.0","id":2,"method":"image.generate","params":{"prompt":"...","aspect":"landscape","count":1,"model":"...","reference":"...","reference_media_ids":[],"seed":42}}
{"jsonrpc":"2.0","id":3,"method":"video.submit","params":{"prompt":"...","aspect":"landscape","duration":8,"model":"...","resolution":"720p","start":"...","end":"...","seed":42}}
{"jsonrpc":"2.0","id":4,"method":"video.poll","params":{"operation_id":"opaque"}}
```

A poll result has this shape:

```json
{"operation_id":"opaque","status":"succeeded","assets":[{"id":"video-1","type":"video","url":"https://...","local_path":"","prompt":"...","width":1280,"height":720,"size":1234,"mime_type":"video/mp4"}],"error":""}
```

Operation IDs are opaque and owned by the adapter that issued them. gflow does
not interpret an ID or send it to another provider. Whether operations survive an
adapter restart is adapter-defined; callers must not assume they do.

## Auth Describe And Login

`gflow login` uses `auth.describe` for guidance and `auth.login` for automation:

```json
{"jsonrpc":"2.0","id":5,"method":"auth.describe","params":{}}
{"jsonrpc":"2.0","id":6,"method":"auth.login","params":{}}
```

Both return:

```json
{"ready":true,"summary":"Ready","checks":[{"name":"session","ok":true,"detail":"OK"}],"next_steps":[]}
```

Guidance-vs-automation principle: adapters declare what gflow can auto-run
versus what the operator must do. `auth.describe` is read-only guidance
(`ready`, `summary`, `checks`, `next_steps` with placeholders only, never
secrets). gflow only invokes `auth.login` when the adapter declares the
`auth-login` capability; otherwise `gflow login` prints `next_steps` and exits.
Calls are bounded and serialized. An adapter without auth methods surfaces an
explicit unsupported error (capability check or JSON-RPC `-32601 Method not
found` mapping), never a hang. Fatal protocol-error semantics are preserved:
malformed or mismatched responses terminate the adapter client.

## Lifecycle And Compatibility

Calls are serialized and responses must carry the matching JSON-RPC ID. Closing
closes stdin and stdout, waits a bounded interval for graceful direct-child exit,
then requests process-tree termination and waits another bounded interval for the
direct child to be reaped. `Close` succeeds only after that direct-child wait
completes; timeout or termination failures are returned and remain stable on later
`Close` calls. Cancelling an in-flight call terminates the adapter and closes its
stdio so blocked readers can return.

On Unix, gflow starts the adapter in a new process group and sends `SIGKILL` to the
group on forced termination. On Windows, the Go standard library has no Job Object
API, so gflow invokes the absolute `%SystemRoot%\\System32\\taskkill.exe` path
directly (never through a shell) with `/T /F`, then kills the direct process as a
fallback. If `taskkill` is unavailable or cannot terminate the tree, the direct
child is still killed and reaped, but descendants may survive. In every platform
case, gflow proves and reports only that the direct child was waited and reaped;
it does not claim every descendant exited.

Compatibility is exact for protocol version 1. Additive result fields may be
ignored, but new methods, capabilities, or incompatible field changes require a
new gflow adapter protocol version. This version is unrelated to the MCP protocol
version used by gflow's MCP server.
