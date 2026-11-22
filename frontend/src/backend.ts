// The one place that reaches the Go side: the generated bindings and the Wails
// runtime. Features import from here, so the long binding paths stand in one
// place and a test setup has a single module to replace.
export {Clipboard, Events} from "@wailsio/runtime";
export {AppService} from "../bindings/github.com/pklaschka/lieferling";
export {type Command, CommandState} from "../bindings/github.com/pklaschka/lieferling/internal/install/models";
export type {Link} from "../bindings/github.com/pklaschka/lieferling/internal/deeplink/models";
export type {ImportItem, ImportPlan, Status} from "../bindings/github.com/pklaschka/lieferling/internal/store/models";
export type {Check, Origin, Report, Sender} from "../bindings/github.com/pklaschka/lieferling/internal/verify/models";
