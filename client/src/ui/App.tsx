import { useApp } from "../app";
import { Auth } from "./Auth";
import { Effects, LOADING } from "./common";
import { Main } from "./Main";

export function App() {
  const app = useApp();
  return (
    <div class={app.effects ? "crt" : "crt no-fx"}>
      {app.effects && <Effects />}
      {app.phase === "loading" && (
        <div class="loading" role="status">
          {LOADING}
          <span class="cursor" aria-hidden="true" />
        </div>
      )}
      {app.phase === "auth" && <Auth />}
      {app.phase === "main" && <Main />}
    </div>
  );
}
