## Implementation Notes

### Objective 1 — SQLite persistence

The in-memory store has been replaced with a SQLite-backed implementation
(`service/SQLiteRecordService`). The database file `records.db` is created
in the working directory on first run and survives server restarts.

The driver is `github.com/mattn/go-sqlite3`, which requires CGo — a C
compiler (gcc/clang on macOS/Linux, MSVC build tools on Windows) must be
available at build time. A pure-Go alternative (`modernc.org/sqlite`) is a
straightforward swap if a CGo-free build is preferred.

### Objective 2 — Time travel / versioning

V2 endpoints are mounted under `/api/v2` and provide read access to a
record's full history. V1 endpoints remain byte-for-byte compatible: same
routes, same request/response shapes, same status codes.

Every write — whether it arrives via v1 or v2 — appends a row to the
`record_versions` table inside the same transaction that updates the
latest-state cache, so history can never diverge from the current state.

#### New endpoints

| Method | Path                                   | Description                              |
|--------|----------------------------------------|------------------------------------------|
| GET    | `/api/v2/records/{id}`                 | Latest version (with version + timestamp)|
| GET    | `/api/v2/records/{id}/versions`        | List all versions of a record            |
| GET    | `/api/v2/records/{id}/versions/{v}`    | Specific historical version              |
| POST   | `/api/v2/records/{id}`                 | Create or update; returns new version    |

#### Design choices

- **Single SQLite file, two tables.** `records` holds the current state
  (used by v1, unchanged). `record_versions` is an append-only history
  table. Reaching for object storage (S3/blob) would be premature for
  records this small; a relational schema also keeps the write path
  transactional.
- **Full snapshots per version, not diffs.** Reads at a point in time
  become a single-row lookup. Storage cost is negligible at this scale;
  diff compaction is a future optimization.
- **Every write creates a version, even no-ops.** For an audit log, the
  act of writing is itself signal — knowing a policyholder re-confirmed
  data at a particular time is information. Skipping "identical" writes
  also requires defining identical, which is its own can of worms.
- **Interface segregation.** The v1 API package depends only on the
  smaller `RecordService` interface and cannot accidentally call
  versioning APIs. V2 depends on `VersionedRecordService`, which embeds
  the former.

#### What I would add next

- **Bitemporal modeling.** The README example (a change in March that
  isn't reported until July) hints at the distinction between *when we
  learned it* and *when it actually became true*. The current schema
  records only the former. A `valid_from` / `valid_to` pair would let
  the system answer both "what did we know on date X" and "what was
  the policy's actual state on date X."
- **Attribution.** A `changed_by` column once auth is in place.
- **Pagination on `/versions`.** Fine at current scale, but a long-lived
  record could have thousands of versions.

# Rainbow - Backend Take-Home Assignment

Please create a private fork of this repo and complete the objectives.
Once you are finished, send us an email with a link to your private repo.


## To Create A Private Fork

1. Clone the repository to your local machine
```bash
git clone git@github.com:rainbowmga/timetravel.git
cd timetravel
```

2. Create a new **private** repository on your GitHub account
- https://github.com/new

3. Add your new private repo as a remote:
```bash
git remote rename origin upstream
git remote add origin https://github.com/YOUR_USERNAME/NEW_PRIVATE_REPO.git
```

4. Push the code to your new private repo:
```bash
git push -u origin master
```


## To Run The Server

1. Compile and run the Go application:
```bash
cd timetravel
go run .
```

2. Test the server using the healthcheck endpoint:
```bash
curl -X POST http://localhost:8000/api/v1/health
```

You should see the following response:
```json
{"ok":true}
```


## The Assignment

A core part of any insurance platform is a reliable and auditable
record-keeping system. It must store all relevant data used to underwrite
policies. Policyholders periodically submit and update information about the
risks they want covered, such as their desired liability limits or changes to
their workforce. These changes can significantly affect the policy's risk
profile and, consequently, the premium.

The current codebase represents a very simplified version of this system, with:
- `GET /api/v1/record/{id}` – retrieves a record (a simple JSON mapping of
strings to strings)
- `POST /api/v1/record/{id}` – creates or updates a record

### The Problem

Maintaining only the *current* state of each record is not enough. For
compliance and proper risk assessment, we must also understand how that state
*evolved*.

Consider the following example. A business buys a policy in January. In March,
they change their business hours, but they don't notify us until July. During
that four-month gap, we are unknowingly covering a risk that has changed.
Depending on the nature of the change, we may need to:
- **Retroactively adjust the premium**
- Or even **void the policy** if the change introduces unacceptable risk

To resolve this, we need a versioned, historical view of the data:
- What did we know and when?
- When did the change actually occur?

### Objective 1: Persist Data with SQLite

Replace the in-memory storage backend with a persistent SQLite database. The
goal is to ensure that all record data is retained even if the server is shut
down and restarted.

### Objective 2: Implement Time Travel Functionality

Introduce a “time travel” feature that allows querying the state of any record
at a specific point in time. This enables accurate reconstructions for
compliance, audits, and risk recalculations.

This objective is open-ended and may require significant changes across the
codebase. You'll introduce **record versioning and history tracking**.

Build out a new set of endpoints under `/api/v2` with the following
functionality:
- Retrieve records at specific versions (not just the latest)
- Apply updates to the latest version while preserving history
- List all available versions of a record
- Ensure full backward compatibility: `/api/v1` endpoints should continue to
work as-is, with no changes in behavior


## Notes on the Assignment

You are free to use any tools, libraries, or frameworks you prefer — even building
the solution in a different programming language if desired.

We expect you to work on this task as if it was a normal project at work. So please write
your code in a way that fits your intuitive notion of operating within best practices.

We recommend making separate commits for each objective to help illustrate how you approached
and broke down the assignment.  Don't hesitate to commit work that you later revise or remove
— it's valuable to see your process evolve over time.

Parts of this assignment are left intentionally ambiguous. How you resolve these
ambiguities will help us understand your decision-making process.

However, if you do have questions, don't hesitate to reach out!

### FAQ

#### Can I use a different language?
Yes! We've had successful submissions in Python, Java, and others. Just make sure the
functionality replicates what's provided in the Go starter code.

#### Did you really end up implementing something like this at Rainbow?
Yes, but unfortunately it wasn't as simple as this in practice. For insurance a
number of requirements force us to maintain historic records across many
different object types. So in fact we implemented this across multiple different
tables in our database. 


## Reference -- The Current API

The current API consists of just two endpoints:
- `GET /api/v1/records/{id}`
- `POST /api/v1/records/{id}`,

All ids must be **positive integers**.

### `GET /api/v1/records/{id}`

Retrieves a record by its ID. If the record exists, the server returns it in
JSON format. If the record does not exist, an error message is returned.

✅ Successful Response Example
```bash
> GET /api/v1/records/2323 HTTP/1.1

< HTTP/1.1 200 OK
< Content-Type: application/json; charset=utf-8

{"id": 2323, "data": {"david": "hey", "davidx": "hey"}}
```

❌ Error Response Example
```bash
> GET /api/v1/records/32 HTTP/1.1

< HTTP/1.1 400 Bad Request
< Content-Type: application/json; charset=utf-8

{"error": "record of id 32 does not exist"}
```

### `POST /api/v1/records/{id}`

Creates or updates a record at the specified ID.
- If the record does not exist, it will be created.
- If the record already exists, it will be updated.
- Payload values must be a JSON object with string keys and values (or `null`).
- Keys with `null` values will be deleted from the record.

✅ Create a Record
```bash
> POST /api/v1/records/1 HTTP/1.1
> Content-Type: application/json

{"hello": "world"}

< HTTP/1.1 200 OK
< Content-Type: application/json; charset=utf-8

{"id": 1, "data": {"hello": "world"}}
```

🔁 Update a Record
```bash
> POST /api/v1/records/1 HTTP/1.1
> Content-Type: application/json

{"hello": "world 2", "status": "ok"}

< HTTP/1.1 200 OK
< Content-Type: application/json; charset=utf-8

{"id": 1, "data": {"hello": "world 2", "status": "ok"}}
```

❌ Delete a field from a record
```bash
> POST /api/v1/records/1 HTTP/1.1
> Content-Type: application/json

{"hello": null}

< HTTP/1.1 200 OK
< Content-Type: application/json; charset=utf-8

{"id": 1, "data": {"status": "ok"}}
```
