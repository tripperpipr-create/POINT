// Порядок снимков квестов и нарядов — одно правило с хостом расширения.
//
// Правило живёт в `vscode-extension/snapshot-order.js`: хост грузит его
// require(), а в поставку `ui/**` не едет. Вебвью берёт тот же файл — esbuild
// собирает CommonJS в бандл, — и расхождения двух копий быть не может.
import snapshotOrder from '../../snapshot-order.js'

export const mergeNewerById = snapshotOrder.mergeNewerById
export const newerBoot = snapshotOrder.newerBoot
export const upsertNewer = snapshotOrder.upsertNewer
export const workOrderStamp = snapshotOrder.workOrderStamp
