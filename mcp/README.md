# Runtime MCP adapter

This optional module generates MCP tools from the same registry/DTOs as OpenAPI.
It uses the official Go MCP SDK, requires Go 1.25+, and leaves the core library's
Go 1.22 requirement unchanged. No generated files or second route list are needed.

Run the local example from this directory:

```sh
go run ./example
```

- API: `http://127.0.0.1:8080`
- MCP Streamable HTTP endpoint: `http://127.0.0.1:8081/mcp`

Connect an MCP client with Streamable HTTP support, list tools, and call the greet
tool with `{"query":{"name":"World"}}`. Tool names combine method/path with a
stable hash to avoid collisions. The example binds to loopback only.

## Integration

After registering routes and calling `swagger.SetRouter`:

```go
h, err := swaggermcp.NewHandler(swaggermcp.Config{
    BaseURL: "http://127.0.0.1:8080", // trusted API address, optional gateway prefix
})
if err != nil { return err }
mux := http.NewServeMux()
mux.Handle("/mcp", h)
server := &http.Server{
    Addr: "127.0.0.1:8081", Handler: mux,
    ReadHeaderTimeout: 5 * time.Second,
}
return server.ListenAndServe()
```

Import `swaggermcp "github.com/avran02/swagger/mcp"`. This directory is a separate
Go module: run its tests separately with `cd mcp && go test -race ./...`.
The local `replace` in go.mod supports developing against the parent checkout;
consumers need published versions of both modules (or their own local replaces).

Tools are a startup snapshot. Restart/rebuild the handler when routes change.
`BaseURL` explicitly selects the backend; OpenAPI `servers` are not used to pick
an environment. Requests pass through the actual API router and its middleware.

## Exposure and identity

By default only GET/HEAD operations become tools. Use `Include` for an explicit
allowlist of method/path pairs, including writes when appropriate. GET is a
convention, not a guarantee of harmless behavior: review selected handlers.

Path/query arguments and JSON bodies become nested `path`, `query`, and `body`
objects. Headers and cookies are deliberately absent from tool input. Set them
in `Authorize(ctx, call, outgoingRequest)` using validated caller identity and
server-side tenant policy. Bearer operations fail closed without this callback.
Returning nil from the callback means your application has authorized the call;
the upstream API still enforces its own rules. Public routes with tenant headers
also need a callback to populate those headers.

MCP endpoint authentication is the application's responsibility. The callback
is not an OAuth server and does not protect tool discovery. For remote access,
wrap the handler with MCP-compatible authentication and Host/Origin validation,
and serve over TLS. Do not blindly forward MCP bearer tokens to a different API:
use credentials issued for that API and scoped to the validated user.

## Limits

- Scalar query/path parameters and JSON bodies are supported. Multipart and
  complex parameters cause construction to fail; exclude those routes explicitly.
- Binary downloads are not converted to MCP resources; exclude them with `Include`.
- JSON Schema checks tool input; the API binder and business logic remain authoritative.
- Response bodies are text with HTTP status. Non-2xx becomes `isError: true`.
- Default response limit: 1 MiB; default HTTP timeout: 30 seconds. Oversized
  responses return an error, not silently truncated data.
- Redirects are not followed. Calls are not automatically retried.
- No built-in approval, idempotency store, per-user tool filtering, rate limits,
  redaction, audit storage, pagination orchestration or transaction workflow.

## Business risks of automatic API-to-MCP conversion

1. **Endpoints are not business tasks.** A goal like “refund an order” can require
   checking eligibility, choosing an amount, authorization and confirmation. A
   one-to-one API wrapper cannot infer that workflow. Use curated task-level tools
   for critical operations and meaningful summaries/field descriptions.
2. **Authority and tenant isolation.** A shared service account can let every
   assistant act as an administrator. Validate the caller, derive tenant scope
   on the server, and enforce permissions in the API on every call. A separate
   port is an operational boundary, not an authorization boundary.
3. **Duplicate or mistaken writes.** Model/client retries can repeat payments,
   messages or deletions even though this adapter does not retry. Implement
   idempotency in the business service and approval for costly actions. MCP
   annotations are hints, not enforcement.
4. **Data disclosure and untrusted content.** API results can contain personal
   data, secrets or text that attempts to steer the assistant. Minimize/redact
   results, enforce field-level access, and treat returned text as data.
5. **Cost and reliability.** Large tool catalogs and responses consume context;
   multi-step calls increase latency and backend load. Start with an allowlist,
   use bounded pagination and quotas, and measure task success, cost and latency.
6. **Accountability and contract drift.** Record caller, tenant, tool, correlation
   ID and outcome without logging credentials. DTO/route changes also change the
   agent contract; test real workflows and version breaking changes.

For a first rollout, expose a small set of useful read operations, then add
writes with explicit permissions, idempotency and business approval rules.
