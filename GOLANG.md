# Golang Rules To Live By

## Language and Version
- Use Go 1.21+ syntax and features
- Leverage generics where appropriate (Go 1.18+)
- Use the standard library whenever possible before reaching for third-party packages
- Follow the official Go style guide and conventions

## Code Style
- Use `gofmt` / `goimports` for formatting (tabs for indentation)
- Follow Effective Go guidelines
- Use descriptive variable and function names
- Use `camelCase` for unexported identifiers
- Use `PascalCase` for exported identifiers
- Use `UPPER_CASE` with underscores for constants only when truly constant and package-level
- Keep functions short and focused on a single responsibility
- Prefer early returns to reduce nesting

## Package Organization
- Use meaningful package names (short, lowercase, no underscores)
- Avoid package name collisions with standard library
- Group related functionality into cohesive packages
- Use `internal/` for packages not meant for external use
- Keep `main` packages minimal - delegate to library packages

## Imports
- Group imports: standard library, third-party, local packages
- Use `goimports` to manage import organization automatically
- Avoid dot imports except in tests where appropriate
- Prefer explicit imports over blank identifier imports unless necessary for side effects

## Error Handling
- Always handle errors explicitly - never ignore with `_`
- Use `errors.New()` or `fmt.Errorf()` for error creation
- Wrap errors with context using `fmt.Errorf("context: %w", err)`
- Use custom error types when additional context is needed
- Check errors immediately after the call that may return them
- Use `errors.Is()` and `errors.As()` for error comparison
- Consider using sentinel errors for expected error conditions

## Documentation
- Write doc comments for all exported functions, types, and packages
- Start doc comments with the name of the element being documented
- Use complete sentences in documentation
- Include examples in `_test.go` files using `Example` functions
- Add inline comments for complex logic
- Document non-obvious behavior and edge cases

## Testing
- Write unit tests for all new functionality
- Use the standard `testing` package
- Name test files with `_test.go` suffix
- Use table-driven tests for comprehensive coverage
- Use `t.Helper()` in test helper functions
- Use `testify` or similar for assertions if it improves readability
- Aim for high test coverage on critical paths
- Use `go test -race` to detect race conditions

## Dependencies
- Use Go modules (`go.mod` and `go.sum`) for dependency management
- Pin dependency versions for reproducibility
- Minimize external dependencies - prefer the standard library
- Vet dependencies for maintenance status and security
- Run `go mod tidy` to clean up unused dependencies
- Document why each significant dependency is needed
- Installs via 'go install github.com/.../...@latest` should always be available (and always documented)

## Versioning
**CRITICAL: Every project MUST have a `version.go` file**
- Always include a `version.go` file in the project root or main package to track the current version
- Use Semantic Versioning (SemVer) format: `MAJOR.MINOR.PATCH` (e.g., `1.2.3`)
  - **MAJOR**: Incremented for incompatible API changes
  - **MINOR**: Incremented for new functionality in a backward-compatible manner
  - **PATCH**: Incremented for backward-compatible bug fixes
- The `version.go` file should export a `Version` constant or variable accessible to the application
- Example structure:
  ```go
  package main
  
  // Version is the current semantic version of the application
  const Version = "0.1.0"
  ```
- Version should be displayed via `--version` or `-v` flag in CLI applications
- Makefile must include version management targets:
  - `make version` - Display current version
  - `make version-increment` - Increment patch version (or prompt for major/minor/patch)
  - `make release` - Create a release (tag, build, changelog update)
- Keep version in sync with git tags when releasing
- Update CHANGELOG.md when version changes
- Baking the version into `version.go` at build time (rather than reading from git tags or external sources) is the standard pattern for this project — the binary always knows its own version without any runtime dependencies

## Concurrency
- Use goroutines and channels appropriately
- Always ensure goroutines can exit (avoid goroutine leaks)
- Use `sync.WaitGroup` to wait for goroutine completion
- Use `sync.Mutex` or `sync.RWMutex` for shared state protection
- Prefer channels for communication, mutexes for state
- Use `context.Context` for cancellation propagation
- Run tests with `-race` flag to detect data races

## Performance
- Profile before optimizing (`pprof`, benchmarks)
- Avoid premature optimization
- Use benchmarks (`func BenchmarkXxx`) to measure performance
- Consider memory allocations in hot paths
- Use `sync.Pool` for frequently allocated objects
- Prefer stack allocation over heap when possible