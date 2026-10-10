import "@fontsource/vt323/latin-400.css";
import "@fontsource/vt323/latin-ext-400.css";
import "./styles.css";
import { render } from "preact";
import { App } from "./ui/App";
import { boot } from "./app";
import { installHotkeys } from "./hotkeys";

render(<App />, document.getElementById("app")!);
installHotkeys();
void boot();
