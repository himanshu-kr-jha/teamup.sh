# 29 — Fix main.go: Services initialization and wiring

**What to build:** Fix the service initialization in main.go to properly wire all services with their Config types and Broadcaster interfaces.

**Blocked by:** 25, 26, 27, 28 — All previous main.go fixes

**Status:** completed

- [x] Initialize services with their specific Config types (converted from unified Config)
- [x] Set up combinedService with chatService and callsService
- [x] Set broadcasters for chatService and callsService (wsHub)
- [x] Initialize presenceService, authService, notificationsService, filesService, tasksService, orgService, wizardService
- [x] Set up chatService and callsService with wsHub as broadcaster
- [x] Register all API routes with correct service instances
- [x] Set up WebSocket endpoint with wsHub and authService

**Type:** task
