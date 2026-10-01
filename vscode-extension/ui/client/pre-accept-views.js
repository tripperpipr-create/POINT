// Проверка Point перед приёмкой: человек видит, что критерии уже прогонялись
// на результате последнего пишущего этапа и чем это кончилось.
export function preAcceptNoteHtml(runtime, esc) {
  const check = runtime?.preAcceptCheck
  if (!check || !Number(check.total)) return ''
  const tail = check.allPassed || runtime.stall ? '' : ' — исполнитель получил причины и исправляет'
  return `<small class="work-order-exec-note" data-pre-accept-check="${check.allPassed ? 'passed' : 'failed'}">Проверки Point перед приёмкой: ${esc(String(Number(check.passed) || 0))} из ${esc(String(Number(check.total)))} прошли${tail}</small>`
}
