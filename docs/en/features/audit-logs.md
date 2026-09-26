# Audit Logs

Audit logs record key operations performed by administrators and sub-users on the panel, for review and compliance auditing.

## What Is Recorded

Each audit log entry contains:

| Field | Description |
| --- | --- |
| Time | When the operation happened. |
| Action | Action identifier, e.g. `container.create`, `node.create`, `policy.create`. |
| Target | The operation target, e.g. container name, node name. |
| Detail | Description of the operation. |
| User | The operator (administrator or `user:<sub-user>`). |
| IP | Source IP of the operation. |
| Result | Success/failure; failure includes an error message. |

## Coverage

- Container create, delete, reinstall, start/stop, password reset.
- Node create, delete, registration.
- Sub-user creation and permission changes.
- API key creation and deletion.
- Policy, storage, routing, snapshot, and other management operations.
- Sensitive operations such as administrator password changes.

The system keeps at most the most recent 500 audit log entries, and persists them to SQLite so they survive restarts.

## Login Logs

The "Audit Logs" page also provides login logs, recording the time, username, source IP, and success status of every login, for detecting unusual sign-ins.

## Related Endpoints

| Method | Path | Description |
| --- | --- | --- |
| GET | `/api/audit-logs` | Audit log list |
| GET | `/api/login-logs` | Login log list |

`/api/v1/audit-logs` and `/api/v1/login-logs` provide versioned endpoints.
