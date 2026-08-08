**Status:** resolved

- [x] Remove duplicate Attachment, Channel, ChannelMember, NewService declarations in chat.go
- [x] Make chat service use WSConnection interface instead of concrete Connection struct
- [x] Fix json.Unmarshal type assertion for msg.Payload (interface{} → []byte)
- [x] Fix conn.UserID() and conn.OrgID() method calls (interface methods)
- [x] Fix conn.Enqueue() method calls
- [x] Ensure Message vs MessageModel types are consistent
- [x] Remove unused websocket import from chat.go
- [x] Fix unused variables in routes.go

**Type:** task
