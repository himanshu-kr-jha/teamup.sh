# 15 - Creating a task with a title over 500 characters returns a 500

Status: resolved
Type: bug

## What is wrong

`PATCH /tasks/:id` validates title length (`titleMaxLength = 500`, trimmed non-empty). `POST /tasks` does not: it only has `binding:"required"`. The column is `VARCHAR(500)` (`migrations/0001_initial_schema.up.sql:248`), so a pasted paragraph fails in the INSERT and the user sees "failed to create task" (HTTP 500, server error logged) instead of a clear 400, and the form's text is lost on some clients. This is separate from the already-known blank-title bug but is the same validation gap; fix them together.

## Where

- `internal/tasks/routes.go:~146` request struct (no length/trim check); `internal/tasks/tasks.go:~395` `CreateTask` inserts as given.
- Update side already correct: `internal/tasks/tasks.go:~660-668`.

## How to reproduce

`internal/tasks/routes_test.go`: `POST /tasks {"title": "<501 x 'a'>"}`. Today: 500. Expected: 400 `invalid title`.

## Simple fix

At the top of `CreateTask` (service), `title = strings.TrimSpace(title)`; if empty or `len(title) > titleMaxLength` return `apperrors.New(apperrors.CodeBadRequest, "invalid title")`. Covers the blank-title bug too.

## Answer

Same fix as ticket 01: `CreateTask` trims the title and returns 400 `invalid title` when it is blank or over 500.
Test: `TestCreatingATaskRefusesBlankAndOverlongTitles` in `internal/tasks/completion_test.go`.
