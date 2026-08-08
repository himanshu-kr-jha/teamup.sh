# 27 — Fix main.go: AuthServiceInterface implementation mismatch

**What to build:** Fix the AuthService to implement the websocket.AuthServiceInterface exactly, including the ValidateAccessToken return type.

**Blocked by:** 25 — Fix main.go: Config struct mismatch

**Status:** completed

- [x] Update auth.Service.ValidateAccessToken to return *websocket.Claims instead of *auth.Claims
- [x] Ensure auth.Claims struct matches websocket.Claims fields exactly
- [x] Update websocket.Claims to include all required fields (UserID, OrgID, Email, Roles, SessionID, AuthProvider, IDToken)

**Type:** task
