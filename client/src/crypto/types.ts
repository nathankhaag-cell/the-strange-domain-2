// The message pane talks to encryption only through this interface. The node
// never sees plaintext: everything sent to /groups/{gid}/messages is an MLS
// message produced here.

export interface ShownMessage {
  seq: number;
  groupId: string;
  senderUser: string;
  senderDevice: string;
  createdAt: number;
  /** ok: decrypted; redacted: deleted on the node; unavailable: this device cannot read it. */
  state: "ok" | "redacted" | "unavailable";
  text?: string;
}

/**
 * ready: this device is in the group's MLS state and can send.
 * waiting: the group exists but this device has not been added yet.
 * empty: nobody has set up encryption for this group yet; sending creates it.
 * unknown: not checked yet.
 */
export type GroupStatus = "ready" | "waiting" | "empty" | "unknown";

export interface MessageCrypto {
  /** Load local state and publish KeyPackages so others can add this device. */
  start(): Promise<void>;
  status(groupId: string): GroupStatus;
  messages(groupId: string): ShownMessage[];
  /** Fetch new messages for a group and decrypt them. */
  sync(groupId: string): Promise<void>;
  /** Encrypt and send a text message, creating the group first if nobody has. */
  send(groupId: string, text: string): Promise<void>;
  /** Show a deleted message as redacted and forget its plaintext. */
  markDeleted(groupId: string, seq: number): Promise<void>;
  /** Join any groups this device has been welcomed into. Returns their ids. */
  takeWelcomes(): Promise<string[]>;
  /** Add devices that belong in the group and remove people who left and revoked devices. */
  reconcile(groupId: string): Promise<void>;
  /** Called after any change the UI should show. */
  onChange(fn: (groupId: string) => void): void;
}
