# Live analysis scheduler

Live discussion-tree analysis is final-transcript driven. The periodic interval
is retained as a durable fallback; partial transcript events remain display-only
and never enter an AI prompt.

## Data flow

```text
final transcript commit
  -> TranscriptIngestService publishes the created final
  -> per-session scheduler evaluates pending input
  -> bounded debounce / cooldown timer
  -> one frozen sequence range (single-flight)
  -> Azure OpenAI live extraction
  -> compare-and-swap live payload persist
  -> completed WebSocket snapshot
```

Every periodic tick also compares the session's durable final transcripts with
the exact `analyzedFinalSegments` keys in the last successful live payload.
This recovers an event notification missed in-process and a final saved before a
backend restart. A repository error leaves the successful watermark unchanged,
so a later tick can retry.

The process owns one scheduler instance and one periodic registration. Startup,
tick, and shutdown logs include `schedulerInstanceId`,
`schedulerRegistrationId`, and a monotonic `tickCount`. A tick has one
evaluation owner: durable recovery performs the deferred per-session
evaluation, so the ticker does not evaluate the same session a second time
after recovery. `Start` is idempotent, and shutdown cancels the registration
and every per-session timer.

## Per-session state machine

| State | Entry | Exit |
| --- | --- | --- |
| `idle` | no substantive pending final | final event or durable recovery |
| `debounce_scheduled` | eligible final is waiting for debounce, cooldown, max-wait, or retry backoff | timer dispatch, replacement by an earlier bounded deadline, or meeting ending |
| `running` | target sequence range has been frozen | successful persist, failure restoration, or stale CAS handling |
| `rerun_pending` | a final arrived while `running` | current run completes, then the remaining range is evaluated once |
| `finalizing` | meeting ending cancels the normal timer and closes tree audit scheduling | sealed live round finishes or is superseded by the finalization barrier, then the finalization flush owns the uncovered range |
| `stopped` | meeting is ended, failed, stale, or finalization has completed | terminal |

The maps, timers, watermarks, and single-flight flags are keyed by session ID.
Different sessions therefore do not share cooldown or running state.

## Eligibility and idempotency

- Empty and partial transcript events are ignored before scheduler state or
  normal INFO decision logs are created. Only a successfully stored final
  transcript notifies the final-event path.
- Filler-only pending input is retained for possible coalescing but does not
  call the provider, even after max wait.
- Input at or above `AI_LIVE_ANALYSIS_MIN_CHARS` runs after debounce and
  cooldown.
- Short substantive input runs no later than
  `AI_LIVE_ANALYSIS_MAX_WAIT_SECONDS`, except that provider-failure backoff is
  still honored.
- The scheduler removes the pending slice and freezes its sequence range before
  starting the provider call.
- Only a successfully persisted CAS result advances
  `analyzedFinalSegments` / `coveredThroughSequenceNo`.
- Provider, schema, and tree-persist failures restore the frozen segments.
- A stale CAS filters the range through the newer durable exact-key watermark
  before retrying.
- A duplicate timer callback is rejected by its per-session generation number.

## Finalization barrier

Every sealed round carries a per-session generation and an operation id. A live
round is one unit of work through its tree reorganization, so the barrier at
meeting end waits for the whole round, not just the extraction call.

When the wait times out, finalization does not abandon the meeting. It marks the
round superseded, records the awaited operation
(`finalization_operation_awaited`), and continues from the latest fully projected
and persisted live snapshot (`finalization_fallback_selected`). A superseded
round that finishes later discards its result instead of persisting or
publishing it (`live_round_superseded`), so it can never rewind the finalized
tree, and `finishLiveRunLocked` ignores it so it cannot release the round that
replaced it.

Coverage-only changes are meaningful: even if the canonical tree, evidence,
and agenda progress are unchanged, advancing `coveredThroughSequenceNo` lets
clients know that the latest final was analyzed. Completion logs separately
report tree, progress, and evidence changes.

## Configuration

| Environment variable | Default | Purpose |
| --- | ---: | --- |
| `AI_LIVE_ANALYSIS_INTERVAL_SECONDS` | 10 s | durable fallback tick |
| `AI_LIVE_ANALYSIS_DEBOUNCE_MILLISECONDS` | 2000 ms | coalesce adjacent finals |
| `AI_LIVE_ANALYSIS_COOLDOWN_SECONDS` | 8 s | bound provider call frequency |
| `AI_LIVE_ANALYSIS_MAX_WAIT_SECONDS` | 18 s | bounded wait for short substantive input |
| `AI_LIVE_ANALYSIS_MIN_CHARS` | 80 | normal input threshold |

Scheduler logs contain instance/registration IDs, tick counts, session IDs,
sequence numbers, counts, timing, state, decisions, and reasons. They never
contain raw transcript text. Timer-triggered evaluation uses
`trigger=debounced_timer`; periodic durable recovery uses
`trigger=periodic_tick`.
