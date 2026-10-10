import { useApp } from "../app";
import { AppUpdate } from "./AppUpdate";
import { Auth } from "./Auth";
import { Connect } from "./Connect";
import { Effects, LOADING } from "./common";
import { Main } from "./Main";
import { TitleBar } from "./TitleBar";

export function App() {
  const app = useApp();
  return (
    <>
      <TitleBar />
      <div class={app.effects ? "crt" : "crt no-fx"}>
        {app.effects && <Effects />}
        {app.phase === "loading" && (
          <div class="loading" role="status">
            {LOADING}
            <span class="cursor" aria-hidden="true" />
          </div>
        )}
        {app.phase === "connect" && <Connect />}
        {app.phase === "auth" && <Auth />}
        {app.phase === "main" && <Main />}
        <AppUpdate />
      </div>
    </>
  );
}
