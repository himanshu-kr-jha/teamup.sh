export type WSMessageType =
  | 'conn.init' | 'conn.ack' | 'conn.ping' | 'conn.pong' | 'conn.close'
  | 'auth.refresh' | 'auth.refreshed'
  | 'task.comment'
  | 'chat.send' | 'chat.sent' | 'chat.message' | 'chat.edit' | 'chat.delete' | 'chat.reaction' | 'chat.typing' | 'chat.read'
  | 'presence.update' | 'presence.changed' | 'presence.sync'
  | 'call.invite' | 'call.invited' | 'call.accept' | 'call.decline' | 'call.ended' | 'call.reach' | 'call.signaling' | 'call.signaling.relay'
  | 'notification.new' | 'notification.read';

export interface Message<T = any> {
  id: string;
  type: WSMessageType;
  payload: T;
  timestamp: string;
  version: number;
}

export const protocol = {
  // Type guards and helpers
  isChatMessage: (msg: Message): msg is Message & { type: 'chat.message' } => msg.type === 'chat.message',
  isPresenceChanged: (msg: Message): msg is Message & { type: 'presence.changed' } => msg.type === 'presence.changed',
  isCallInvite: (msg: Message): msg is Message & { type: 'call.invite' } => msg.type === 'call.invite',
};
