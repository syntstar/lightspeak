import { mount } from "svelte";
import "./styles/base.css";
import App from "./App.svelte";
import { loadFonts } from "./lib/fonts";
import { initLog } from "./lib/log";
import { loadPrefs } from "./lib/prefs.svelte";
import { initAppearance } from "./lib/theme.svelte";
import { isWeb } from "./lib/native";
import { suppressNativeMenu } from "./lib/ui/nativeMenu";

initLog();
suppressNativeMenu(isWeb);
loadFonts();
// Apply stored appearance before the first paint of the real UI.
await loadPrefs();
initAppearance();

const target = document.getElementById("app");
if (!target) throw new Error("missing #app");
export default mount(App, { target });
