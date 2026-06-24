# Go Style Guide

Based on Effective Go + Go team conventions, augmented with modern tooling
and patterns for production systems (Go 1.21+).

---

## 1. Formatting & Tooling

**This is non-negotiable: run `gofmt` and `golangci-lint` before every commit.**

`gofmt` handles formatting. `golangci-lint` catches everything else.

Minimal `.golangci.yml` for the repo root:
```yaml
linters:
  enable:
    - errcheck       # ensure errors are handled
    - govet          # reports suspicious constructs
    - staticcheck    # comprehensive static analysis
    - gosimple       # simplification suggestions
    - unused         # unused code detection
    - goimports      # import grouping + formatting
    - misspell       # spelling in comments/strings
    - exhaustive     # exhaustive enum switches
    - wrapcheck      # ensure errors from external packages are wrapped
    - contextcheck   # ensure context is passed correctly

linters-settings:
  goimports:
    local-prefixes: github.com/your-org  # replace with your module path

issues:
  exclude-use-default: false
```

Run in CI:
```bash
golangci-lint run ./...
```

**Import grouping** (enforced by `goimports`):
```go
import (
    // 1. stdlib
    "context"
    "fmt"
    "net/http"

    // 2. third-party
    "github.com/some/library"

    // 3. internal
    "github.com/your-org/your-project/internal/config"
)
```

---

## 2. Naming

- **`MixedCaps`** for all identifiers. No underscores (except test files `_test.go`).
- **Package names:** short, lowercase, single word. `auth`, `storage`, `config`.
  Never `authPackage`, `utils`, `helpers`, `common`.
- **Exported** = uppercase first letter (public). **Unexported** = lowercase (package-private).
- **Acronyms are all-caps or all-lowercase:** `userID`, `parseURL`, `HTTPClient`,
  `getID` — never `UserId`, `parseUrl`, `HttpClient`.
- **Getters** have no `Get` prefix: `user.Name()` not `user.GetName()`.
- **Interface names** = method name + `-er`: `Reader`, `Writer`, `Closer`, `Stringer`.
  Multi-method interfaces: pick a clear noun (`Repository`, `Cache`, `Publisher`).
- **Error variables** prefixed with `Err`: `var ErrNotFound = errors.New("not found")`.
- **Error types** suffixed with `Error`: `type ValidationError struct { ... }`.

---

## 3. Error Handling

**Errors are values. Never discard them silently.**

### Wrapping errors (Go 1.13+)

Always wrap errors with context when propagating up the call stack:
```go
// ✅ — adds context, preserves the original for errors.Is/As
if err := db.QueryRow(ctx, query).Scan(&user); err != nil {
    return fmt.Errorf("get user %d: %w", id, err)
}

// ❌ — loses the original error type, caller can't inspect it
return fmt.Errorf("failed to get user: %v", err)

// ❌ — no context, caller has no idea where this came from
return err
```

### Checking wrapped errors

```go
// Check type with errors.Is / errors.As — works through wrap chains
if errors.Is(err, sql.ErrNoRows) {
    return nil, ErrNotFound
}

var validErr *ValidationError
if errors.As(err, &validErr) {
    // handle specifically
}
```

### Sentinel errors

```go
// Define at package level
var (
    ErrNotFound   = errors.New("not found")
    ErrForbidden  = errors.New("forbidden")
    ErrBadRequest = errors.New("bad request")
)
```

### Never ignore errors

```go
// ❌ — silent failure
rows, _ := db.Query(ctx, query)

// ✅ — explicit handling
rows, err := db.Query(ctx, query)
if err != nil {
    return fmt.Errorf("query users: %w", err)
}
defer rows.Close()
```

### Panic

Reserve for programmer errors only — index out of bounds, nil dereference in
init. Never panic in library code. Always recover at the top-level HTTP/gRPC
handler boundary.

---

## 4. context.Context

**Every function that does I/O, makes a network call, or could block must
accept `context.Context` as its first parameter.**

```go
// ✅ correct signature
func (s *UserService) GetByID(ctx context.Context, id int64) (*User, error) {
    ...
}

// ❌ no context — can't be cancelled, can't carry deadlines
func (s *UserService) GetByID(id int64) (*User, error) {
    ...
}
```

Rules:
- `ctx` is always the **first parameter**, never embedded in a struct.
- Pass `context.Background()` only at the top of the call stack (main, server handler).
- Use `context.WithTimeout` / `context.WithDeadline` for operations with known SLOs.
- Use `context.WithValue` **sparingly** — only for request-scoped metadata
  (trace ID, auth claims). Never for passing business logic dependencies.

```go
// Timeout example
ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
defer cancel()

result, err := s.db.QueryRow(ctx, query)
```

---

## 5. Interfaces & Dependency Injection

- **Define interfaces at the consumer, not the producer.** The package that
  *uses* the interface defines it. This keeps packages independent.
  ```go
  // In the auth package (consumer)
  type UserStore interface {
      GetByID(ctx context.Context, id int64) (*User, error)
  }
  ```
- **Keep interfaces small.** Single-method interfaces are the ideal. Split large
  interfaces into composable smaller ones.
- **Accept interfaces, return structs.** Functions should accept the minimum
  interface they need; return concrete types so callers can use the full API.

---

## 6. Concurrency

- **Don't share memory; communicate via channels.** If two goroutines need the
  same data, send it through a channel.
- **Protect shared state with `sync.Mutex` when channels are overkill.** Be
  explicit: embed `mu sync.Mutex` in the struct next to the fields it protects.
- **Always handle goroutine lifecycles.** Every goroutine you start must have
  a clear way to stop (done channel, `context.Done()`, `sync.WaitGroup`).
- **Use `errgroup`** (golang.org/x/sync/errgroup) for fan-out tasks where you
  need to collect errors from multiple goroutines.

---

## 7. Testing

### Table-driven tests (standard pattern)

```go
func TestParseConfig(t *testing.T) {
    tests := []struct {
        name    string
        input   string
        want    Config
        wantErr bool
    }{
        {
            name:  "valid config",
            input: `{"port": 8080}`,
            want:  Config{Port: 8080},
        },
        {
            name:    "missing port",
            input:   `{}`,
            wantErr: true,
        },
        {
            name:    "invalid json",
            input:   `not json`,
            wantErr: true,
        },
    }

    for _, tc := range tests {
        t.Run(tc.name, func(t *testing.T) {
            got, err := ParseConfig(tc.input)
            if (err != nil) != tc.wantErr {
                t.Fatalf("wantErr=%v, got err=%v", tc.wantErr, err)
            }
            if !tc.wantErr && got != tc.want {
                t.Errorf("got %+v, want %+v", got, tc.want)
            }
        })
    }
}
```

### Test naming

- Unit tests: `TestFunctionName_Scenario` — e.g. `TestGetUser_NotFound`
- Integration/E2E: file suffix `_integration_test.go`, build tag `//go:build integration`

### Test tooling

- Use `testify/assert` and `testify/require` for assertions. `require` stops
  the test immediately; `assert` continues.
- Use `gomock` or `mockery` for interface mocks. Generate, don't hand-write.
- Race detector in CI: `go test -race ./...`

---

## 8. Project Structure

Standard layout for a backend service:
```
myservice/
├── cmd/
│   └── server/
│       └── main.go          # entry point only, wires dependencies
├── internal/
│   ├── config/              # config loading + validation
│   ├── domain/              # core business types (User, Order, etc.)
│   ├── repository/          # data layer (DB, cache)
│   ├── service/             # business logic
│   └── handler/             # HTTP/gRPC handlers
├── pkg/                     # reusable packages safe to import externally
├── migrations/              # SQL migrations
├── .golangci.yml
├── go.mod
└── go.sum
```

- `internal/` is enforced by Go toolchain — nothing outside this module can import it.
- `main.go` should be thin: parse config, wire dependencies, start server.
- Business logic lives in `service/`, not in `handler/`.

---

## 9. Go Modules

- Always specify the minimum Go version in `go.mod`.
- Use `go mod tidy` after adding/removing dependencies.
- Pin indirect dependencies — they appear in `go.sum`.
- Prefer well-maintained modules. Check last commit date before adding.

*Primary source: [Effective Go](https://go.dev/doc/effective_go), [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments)*
