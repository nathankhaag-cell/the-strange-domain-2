// The phone app's first screen: which node to connect to. Browsers and the
// desktop app never show it (see node.ts).

import { useState } from "preact/hooks";
import { chooseNode, lastNode, normalizeNodeAddress } from "../node";
import { HeroMark, useSubmit } from "./Auth";
import { Field } from "./common";

export function Connect() {
  const [address, setAddress] = useState(() => lastNode().replace(/^http:\/\//, ""));
  const s = useSubmit(() => chooseNode(normalizeNodeAddress(address)));
  return (
    <div class="auth">
      <section class="auth-hero" aria-label="The Strange Domain">
        <HeroMark />
      </section>
      <section class="auth-panel" aria-label="Connect to a node">
        <div class="auth-card">
          <form class="auth-form" onSubmit={s.onSubmit}>
            <h2>Connect to a node</h2>
            <p class="muted">
              Enter the address of the Domain Node you use, for example 192.168.1.20:8743 or https://node.example.com.
            </p>
            <Field id="node" label="Node address" value={address} onInput={setAddress} required placeholder="192.168.1.20:8743" />
            {s.err && <div class="error" role="alert">{s.err}</div>}
            <button type="submit" class="btn-primary" disabled={s.busy}>
              Connect
            </button>
          </form>
        </div>
      </section>
    </div>
  );
}
