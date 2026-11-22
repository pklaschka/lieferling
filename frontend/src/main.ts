import "@fontsource-variable/archivo/wdth.css";
import {AppService} from "./backend";
import {setupAdd} from "./features/add";
import {setupAppActions} from "./features/app-actions";
import {setupCatalog} from "./features/catalog";
import {setupCommandLine} from "./features/commandline";
import {setupCustoms} from "./features/customs";
import {setupDeeplink, takeLink} from "./features/deeplink";
import {setupInstall} from "./features/install";
import {load, setupList} from "./features/list";
import {setupPolicy} from "./features/policy";
import {setupRemove} from "./features/remove";
import {$} from "./lib/dom";

// Every feature wires itself up here, before the first row is built: rows
// look up their menu entries and sections by name when they are created.
setupList();
setupInstall();
setupCustoms();
setupPolicy();
setupAppActions();
setupAdd();
setupRemove();
setupDeeplink();
setupCatalog();
setupCommandLine();

AppService.ConfigPath().then((p) => ($("#config-path").textContent = p));
load().then(takeLink);
