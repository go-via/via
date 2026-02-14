# Dead Code Analysis Report

**Date:** 2026-02-13  
**Project:** Via (Go web framework)

---

## Summary

This report identifies dead code using staticcheck analysis.

---

## Findings

### SAFE TO DELETE

| File | Line | Issue | Severity |
|------|------|-------|----------|
| `action.go` | 39-47 | `withSignalOpt` type and `apply` method - unused implementation | LOW |
| `plugins/picocss/pico.go` | 187-189 | `themePath` variable assigned but never used | LOW |

### CAUTION (Style/Warnings)

| File | Line | Issue | Severity |
|------|------|-------|----------|
| `plugins/picocss/pico.go` | 224 | `strings.Title` deprecated since Go 1.18 | MEDIUM |
| `cfg_test.go` | 54 | Should merge variable declaration | LOW |
| `session_test.go` | 54 | Unused value | LOW |

---

## Actions Taken

### 1. Remove unused `withSignalOpt` in action.go

**Before:**
```go
type withSignalOpt struct {
    signalID string
    value    string
}

func (o withSignalOpt) apply(opts *triggerOpts) {
    opts.hasSignal = true
    opts.signalID = o.signalID
    opts.value = o.value
}
```

**Status:** REMOVED

### 2. Remove unused `themePath` variable in picocss plugin

**Before:**
```go
themePath := opts.DefaultTheme
if opts.Classless {
    themePath = "classless/" + themePath
}
```

**Status:** REMOVED

---

## Verification

All tests pass after removals.

