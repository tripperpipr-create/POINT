import { icon } from './ui-icons.js'
import { masterMessageBytes } from './master-compose.js'

// Пределы набора контекста. Их трое и они из разных мест: 16 вложений и 12 000
// знаков ставит расширение (master-context-controller.js), не более четырёх
// изображений за ход — ядро (internal/app/master_attachments.go). Композер
// обязан знать все три заранее: до этой правки он знал только первый, а про
// остальные узнавал отказом уже после нажатия «Отправить».
const CONTEXT_CHARS_LIMIT = 12000
const CONTEXT_IMAGE_LIMIT = 4
const CONTEXT_ITEM_LIMIT = 16

let contexts = {}
const key = id => id || 'legacy'
export function bindMasterContexts(saved) { contexts = saved }
export function receiveMasterContext(message) {
  const id=key(message.conversationId), items=contexts[id] ||= []
  const incoming=(message.contexts || [message.context]).filter(Boolean)
  if(items.length+incoming.length>CONTEXT_ITEM_LIMIT)throw new Error(`Не более ${CONTEXT_ITEM_LIMIT} вложений`)
  // Предел картинок ставит ядро (internal/app/master_attachments.go), и до сих
  // пор интерфейс о нём не знал: пятая уходила вместе с репликой и возвращалась
  // отказом уже из запуска хода, когда выбирать заново поздно.
  const images = items.filter(value => value.kind === 'image').length
    + incoming.filter(value => value?.kind === 'image').length
  if (images > CONTEXT_IMAGE_LIMIT) throw new Error(`Не более ${CONTEXT_IMAGE_LIMIT} изображений за ход`)
  for (const value of incoming) {
    if (!value) continue
    if (items.length>=CONTEXT_ITEM_LIMIT) throw new Error(`Не более ${CONTEXT_ITEM_LIMIT} вложений`)
    items.push({...value,id:value.id || 'att_'+Date.now().toString(36)+Math.random().toString(36).slice(2),kind:value.kind || 'file'})
  }
}
export function clearMasterContext(id) { delete contexts[key(id)] }
// Кнопка «@ Контекст» стоит в нижнем ряду, вместе с режимами: вложений чаще
// всего нет, и ряд над полем ради одной кнопки держал пустую строку в каждом
// разговоре. Ряд вложений остаётся, но появляется вместе с вложениями.
//
// Дверь одна, источников два. Прежде рядом стояли две кнопки — «Открытый
// файл» и «@ Контекст», — и обе делали одно: добавляли контекст, различаясь
// только тем, откуда его брать. Две подписи занимали 178 пикселей ряда и
// заставляли выбирать дверь до того, как человек решил, что именно добавляет.
//
// Плата названа честно: открытый файл был в один клик, стал в два. Быстрый
// путь к нему остаётся — «@» прямо в поле и перетаскивание мышью, — и в
// списке он стоит первым.
//
// Обработчик «взять открытый файл или выделение» написан в расширении
// целиком (master-chat-controller.js); действия те же, меняется разметка.
export function masterContextAddHtml(id, esc, sending) {
  const session = esc(key(id))
  const off = sending ? 'disabled' : ''
  return `<details class="hall-context-menu">
    <summary class="hall-context-add" title="Файлы, папки, ошибки, git diff и буфер терминала. То же открывает «@» в поле; файл можно перетащить мышью">${icon('attach')}<span>Контекст</span></summary>
    <div role="group" aria-label="Что добавить в контекст">
      <button type="button" class="hall-chip" data-action="master-context-attach" data-session="${session}" ${off} title="Взять открытый файл или выделение в редакторе">Открытый файл</button>
      <button type="button" class="hall-chip" data-action="master-context-pick" data-session="${session}" ${off}>Выбрать источник…</button>
    </div>
  </details>`
}
// Сколько весит вложение. У картинки содержимое лежит base64, и её длина — не
// размер файла: четыре знака кодировки несут три байта.
const attachmentBytes = value => {
  const raw = String(value?.content || '')
  if (value?.kind === 'image') return Math.round(raw.length * 3 / 4)
  return masterMessageBytes(raw)
}
const sizeLabel = value => {
  const bytes = attachmentBytes(value)
  if (!bytes) return ''
  return bytes < 1024 ? `${bytes} Б` : `${(bytes / 1024).toFixed(1).replace('.', ',')} КБ`
}

// Сколько контекста уже занято. Предел знаков приходит с двух сторон: 12 000
// режет расширение, а своё число по окну модели считает ядро и присылает как
// contextBudgetChars. Показываем меньший — иначе счётчик обещал бы то, чего
// расширение не пропустит.
function masterContextUsage(id, budgetChars) {
  const items = contexts[key(id)] || []
  const core = Number(budgetChars) > 0 ? Number(budgetChars) : CONTEXT_CHARS_LIMIT
  return {
    items: items.length,
    images: items.filter(value => value.kind === 'image').length,
    chars: items.filter(value => value.kind !== 'image').reduce((sum, value) => sum + [...String(value.content || '')].length, 0),
    limit: Math.min(CONTEXT_CHARS_LIMIT, core),
    imageLimit: CONTEXT_IMAGE_LIMIT,
    itemLimit: CONTEXT_ITEM_LIMIT,
  }
}

export function masterContextHtml(id, esc, sending, budgetChars) {
  const items = contexts[key(id)] || []
  if (!items.length) return ''
  // Путь тусклее имени: во вложении читают имя файла, а папки — только чтобы
  // отличить два одинаковых имени. Целиком серой строкой не читалось ни то, ни
  // другое.
  const split = name => {
    const at = String(name || '').lastIndexOf('/')
    return at < 0 ? { dir: '', base: String(name || '') } : { dir: String(name).slice(0, at + 1), base: String(name).slice(at + 1) }
  }
  // У картинки — настоящая миниатюра из того же base64, что уже лежит во
  // вложении: имя файла о содержимом снимка не говорит ничего. Данные
  // проверяются перед вставкой, как и в ленте: чужой mime и чужие знаки в
  // адрес data: не попадают.
  const preview = value => value.kind === 'image'
      && /^image\/(png|jpeg|webp)$/.test(String(value.mime || ''))
      && /^[A-Za-z0-9+/=]+$/.test(String(value.content || ''))
    ? `<img class="hall-context-thumb" src="data:${esc(value.mime)};base64,${esc(value.content)}" alt="" aria-hidden="true">`
    : ''
  // Имя стоит своим узлом: голый текст внутри кнопки-флекса не умеет ни
  // сжиматься, ни ставить многоточие, и длинное имя срезало вес на полуслове
  // («4,7» без «КБ»). Полное имя — в подсказке: в чипе его может не хватить.
  const rows = items.map(value => {
    const parts = split(value.name)
    const size = sizeLabel(value)
    return `<span class="hall-context-file is-${value.kind === 'image' ? 'image' : 'file'}"><button type="button" data-action="master-context-preview" data-session="${esc(key(id))}" data-id="${esc(value.id)}" title="${esc(value.name)} · посмотреть вложение" ${sending ? 'disabled' : ''}>${preview(value)}${parts.dir ? `<em>${esc(parts.dir)}</em>` : ''}<span>${esc(parts.base)}</span>${size ? `<small>${esc(size)}</small>` : ''}</button><button type="button" data-action="master-context-remove" data-session="${esc(key(id))}" data-id="${esc(value.id)}" aria-label="Убрать ${esc(value.name)}" title="Убрать вложение" ${sending ? 'disabled' : ''}>×</button></span>`
  }).join('')
  // Счётчик появляется, когда запас кончается, и краснеет, когда кончился.
  // До этого он шум — но молчать до самой отправки нельзя: отказ «доступно
  // столько-то» приходил уже после нажатия, когда выбирать заново поздно.
  const usage = masterContextUsage(id, budgetChars)
  const tight = usage.chars >= usage.limit * 0.6
    || usage.images >= usage.imageLimit
    || usage.items >= usage.itemLimit - 2
  const over = usage.chars > usage.limit || usage.images > usage.imageLimit || usage.items > usage.itemLimit
  const budget = tight
    ? `<small class="hall-context-budget${over ? ' is-over' : ''}" title="Ядро принимает до ${usage.limit} знаков текста, до ${usage.imageLimit} изображений и до ${usage.itemLimit} вложений за ход">${usage.chars} / ${usage.limit}${usage.images ? ` · картинки ${usage.images} / ${usage.imageLimit}` : ''}</small>`
    : ''
  return `<div class="hall-context">${rows}${budget}</div>`
}
export function masterContextPayload(id) { return contexts[key(id)] || [] }
export function handleMasterContextAction({action,target,vscode,sending}) {
  if (!action?.startsWith('master-context-')) return false
  if(action==='master-context-source'){vscode.postMessage({type:'openFile',path:target.dataset.path,line:Number(target.dataset.line)||1});return true}
  if (sending) return true
  if (action === 'master-context-attach' || action==='master-context-pick') vscode.postMessage({type:action==='master-context-pick'?'pickMasterContext':'attachMasterContext',conversationId:target.dataset.session})
  else if(action==='master-context-preview') {const value=(contexts[key(target.dataset.session)] || []).find(v=>v.id===target.dataset.id);if(value)vscode.postMessage({type:'previewMasterContext',context:value})}
  else {contexts[key(target.dataset.session)]=(contexts[key(target.dataset.session)] || []).filter(v=>v.id!==target.dataset.id);target.closest('.hall-context-file')?.remove()}
  return true
}

export function installMasterDropzone(root, current, changed, failed, sending) {
  const add = async files => {
    // Во время хода вложение принять нельзя: набор контекста очищается вместе с
    // ходом (clearMasterContext на turnFinished), и принятый сейчас файл исчез
    // бы через секунду — молча, уже после того, как его перетащили.
    if (sending?.()) { failed('Мастер ещё отвечает — вложение можно добавить, когда ход закончится.'); return }
    const conversationId=current(),contexts=[]
    try {
      for (const file of files) {
        if(file.size>5*1024*1024) throw new Error('Вложение больше 5 МБ: '+file.name)
        const image=['image/png','image/jpeg','image/webp'].includes(file.type)
        if(!image && file.size>48000) throw new Error('Выберите фрагмент файла: '+file.name)
        const content=image?await new Promise((resolve,reject)=>{const reader=new FileReader();reader.onload=()=>resolve(String(reader.result).split(',')[1]);reader.onerror=reject;reader.readAsDataURL(file)}):await file.text()
        contexts.push({name:file.name,kind:image?'image':'file',mime:file.type,content})
      }
      receiveMasterContext({conversationId,contexts})
      changed()
    } catch(error) {failed(error.message)}
  }
  // Перетаскивание отвечает, что файл здесь возьмут. Раньше `dragover` делал
  // один preventDefault: курсор показывал «сюда нельзя», цель не подсвечивалась,
  // и понять, куда именно тащить, можно было только по отпущенному файлу.
  const zone = () => root.querySelector?.('.hall-compose')
  const mark = on => { const form = zone(); if (form?.classList) form.classList.toggle('is-dropping', on) }
  root.addEventListener('dragover',event=>{
    if(!event.target.closest?.('.hall-compose'))return
    event.preventDefault()
    if(event.dataTransfer)event.dataTransfer.dropEffect=sending?.() ? 'none' : 'copy'
    mark(!sending?.())
  })
  root.addEventListener('dragleave',event=>{if(event.target.closest?.('.hall-compose'))mark(false)})
  root.addEventListener('drop',event=>{if(!event.target.closest?.('.hall-compose'))return;event.preventDefault();mark(false);void add([...event.dataTransfer.files])})
  root.addEventListener('paste',event=>{if(event.target.id!=='master-input')return;const files=[...(event.clipboardData?.files || [])];if(files.length){event.preventDefault();void add(files)}})
}

export function masterMessageAttachmentsHtml(items, esc) {
  if(!items?.length)return ''
  return `<details class="hall-message-context"><summary>Контекст · ${items.length}</summary>${items.map(v=>`<section><strong>${esc(v.name)}</strong>${v.path?`<button type="button" class="hall-chip" data-action="master-context-source" data-path="${esc(v.path)}" data-line="${Number(v.startLine)||1}">Открыть файл · ${Number(v.startLine)||1}–${Number(v.endLine)||Number(v.startLine)||1}</button>`:''}${v.kind==='image'&&/^image\/(png|jpeg|webp)$/.test(v.mime)&&/^[A-Za-z0-9+/=]+$/.test(v.content || '')?`<img src="data:${v.mime};base64,${v.content}" alt="${esc(v.name)}" />`:`<pre>${esc(v.content || '')}</pre>`}${v.sha256?`<small>Версия снимка: ${esc(v.sha256.slice(0,12))}</small>`:''}</section>`).join('')}</details>`
}
