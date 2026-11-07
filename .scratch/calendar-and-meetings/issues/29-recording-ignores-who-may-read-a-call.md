# Recording ignores who may read a call, and a reopened call overwrites its recording

Status: ready-for-human
Severity: Medium where an Egress service is deployed, Low otherwise

`docker-compose.prod.yml` has no Egress service, so in that stack `StartRoomCompositeEgress` fails and both of these wait until one is added. `docker-compose.yml` has it.

## (a) A manager with no link to a private call can record it and read it

`internal/calls/routes.go:468` checks only the role `owner`, `admin` or `manager`, and `internal/calls/calls.go:497` only the organization.
Decisions 7, 11 and 28 say a manager gets a call's details only with authority over one of its members, and nobody walks in by rank.

Read from the code. Not run, as it needs Egress.
1. A manager of an unrelated team has a link to a private call between two people on other teams.
2. `GET /calls/:id` gives them only `id`, `status`, `callType` and `canKnock`, as it should.
3. `POST /calls/:id/record` starts composing that room into a file, and its response at `routes.go:486` is the full `CallModel`: title, participants and `recordingUrl`.
4. `DELETE /calls/:id/record` also lets them stop somebody else's recording.

Suggested minimal fix: for record and stop, require `MayJoin`, or an org admin. Answer with the same 404 as `GetCallLog` otherwise, and return the limited shape from `GetCall` for anybody who may not read the log.

## (b) Reopening overwrites the file

Before this feature a call could not come back. Now a meeting or occurrence reopens on the same row (decision 23).
The object key is fixed per call: `org-<org>/recordings/<call>/<call>.mp4` (`calls.go:515`).
`recording_status = 'completed'` does not block another start (`calls.go:505`).

So a scrum recorded, emptied and reopened the same day, then recorded again, replaces the first file in object storage, and `recording_url` points at the second.
Read from the code. Not run, as it needs Egress.

Suggested minimal fix: add a timestamp or the egress id to the key.

## Comments

2026-11-07, senior developer:
(a) `mayRecord` in `internal/calls/routes.go` requires the rank and `MayJoin` or org admin, and answers 404 otherwise. `StartRecording` answers with `callAsSeenBy`, the same shape as `GET /calls/:id`.
(b) The object key now has a timestamp.
Verified with a fake LiveKit: `TestAManagerWithNoLinkToAPrivateCallCannotRecordIt`, `TestAManagerOnTheCallAndAnAdminMayRecordIt`, `TestAManagerRecordingAStandingMeetingGetsOnlyWhatTheyMayRead`, `TestRecordingAReopenedCallDoesNotOverwriteTheFirstFile`.
Not verified against a real Egress service, which is not in the test services or `docker-compose.prod.yml`.
