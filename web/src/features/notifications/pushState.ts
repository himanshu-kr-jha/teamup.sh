/**
 * Whether this browser is subscribed to push, as last found out. Kept outside
 * React so a notification can ask without a hook: usePushSubscription keeps it
 * up to date, and it starts out false, so until then the page notifies.
 */
let on = false;

export function pushIsOn() {
  return on;
}

export function setPushOn(value: boolean) {
  on = value;
}
