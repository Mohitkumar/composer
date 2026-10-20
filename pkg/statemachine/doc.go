// Package statemachine is the workflow engine's decision core: a pure state
// machine that turns events about a workflow run into the actions that move
// the run forward.
//
// # Model
//
//	        events                      Machine.Apply                 actions
//	┌──────────────────────┐   ┌───────────────────────────┐   ┌──────────────────────┐
//	│ RunStarted           │   │ definition (WorkflowDAG)  │   │ CreateTask           │
//	│ NodeCompleted        │──▶│ + run snapshot (Run)      │──▶│ DispatchTask         │
//	│ NodeFailed           │   │ ⇒ decide & evolve         │   │ CompleteTask …       │
//	│ TimerFired           │   └───────────────────────────┘   │ FinishRun            │
//	│ SignalReceived       │                                   └──────────────────────┘
//	│ RunRetried           │
//	│ RunCancelled         │
//	└──────────────────────┘
//
// An Event is something that happened to a run: a worker reported a result,
// a timer fired, an API call arrived. The Machine consumes one event against
// the run's current snapshot and returns a Transition: the ordered Actions
// that must be persisted, each already applied to the snapshot. The machine
// performs no writes and no dispatching — the coordinator (pkg/coord) is the
// shell that loads the snapshot under the run's lock, calls Apply, and
// persists the actions in the same transaction.
//
// # Node lifecycle
//
// Each node of the definition gets at most one task per run:
//
//	worker node:   PENDING ──lease──▶ RUNNING ──NodeCompleted──▶ COMPLETED
//	                  ▲                  │
//	                  └──── RetryTask ───┤ NodeFailed, attempts left
//	                                     └─ NodeFailed, no attempts left ─▶ FAILED
//	wait node:     WAITING ──TimerFired / SignalReceived──▶ COMPLETED
//	                       └─ TimerFired (event timeout) ──▶ FAILED
//	control node:  created COMPLETED (SWITCH, FORK, JOIN resolve inline)
//	unreachable:   created SKIPPED (on a branch no SWITCH selected)
//
// A node becomes ready once every incoming edge is resolved. An edge is active
// when its source completed (and, for a SWITCH, selected this target), and
// inactive when its source was skipped or the SWITCH chose another branch. A
// ready node runs if any incoming edge is active and is skipped otherwise.
// This one rule gives fork/join, diamonds and SWITCH re-convergence a uniform
// meaning regardless of branch length.
//
// # Run lifecycle
//
//	RUNNING ──(no task active, none failed)──▶ SUCCEEDED
//	   │    ──(no task active, some failed)──▶ FAILED
//	   └──RunCancelled──▶ FAILED
//	FAILED/SUCCEEDED ──RunRetried──▶ RUNNING
//
// After every event the machine checks whether any task is still active; if
// none is, it finishes the run.
//
// External events delivered to WAIT_FOR_EVENT nodes are called signals here,
// to keep them apart from the machine's own events.
package statemachine
