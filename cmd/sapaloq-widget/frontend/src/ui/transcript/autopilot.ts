/** True for SapaLOQ-authored autopilot nudges (status events), not assistant text. */
export function isAutopilotNudge(status: string): boolean {
  return status.trim().toLowerCase().startsWith('continuing');
}
