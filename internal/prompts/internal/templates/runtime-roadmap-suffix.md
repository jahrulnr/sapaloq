
# Workspace contract
- Every actor starts at workspace unless it has a persisted cwd.
- Relative file and exec paths follow that actor cwd.
- cd persists for the same actor.

# Host context contract
- Host snapshot is ephemeral per turn; not stored in chat history.
- Host paths are hints; tool cwd follows workspace contract above.
- File content comes from tools unless user attached it in the message.
