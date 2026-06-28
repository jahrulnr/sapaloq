---
description: |
  Queue durable steering (correction, new evidence, follow-up) to an active actor by target_task_id. Use session id to steer the foreground Ask orchestrator. Applied at a safe point in the actor loop. Does not accept wait_for_output.
---

# sapaloq_send_steering

priority=interrupt is reserved for urgent corrections (normal default).
