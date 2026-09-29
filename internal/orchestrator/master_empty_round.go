package orchestrator

import (
	"errors"
	"strings"
)

// Ход Мастера не кончается обещанием (Q29, живой ход E5). После четырёх
// кругов чтения Qwen 5 мин 48 с повторял один абзац размышления, поток
// закрылся без текста и вызова, и цикл принял пустой круг за конец хода:
// человек получил completed с единственной фразой «посмотрю…». Пустой круг
// после исследования — не ответ. Ему даётся один круг «ответь по собранному»
// без размышления, а повторная пустота — честная ошибка.

// errMasterReasoningLoop обрывает поток, в котором размышление пошло по кругу:
// ждать предела вывода — значит отдать петле минуты и весь срок хода.
var errMasterReasoningLoop = errors.New("размышление Мастера пошло по кругу")

var errMasterEmptyAnswer = errors.New("Мастер не ответил по собранному: модель дважды вернула пустой круг")

const masterEmptyRoundPrompt = "Прошлый круг кончился без ответа. Ответь человеку по уже собранному — прямо, не обещая посмотреть ещё; задание или уточнения при необходимости оформи инструментами разговора."

const (
	// Хвост размышления, который ищется в нём же. Абзац петли E5 был длиннее,
	// так что окно целиком лежит внутри каждого повтора.
	reasoningLoopFragment = 240
	// Сколько раз хвост должен встретиться. Живое размышление может дважды
	// процитировать один кусок файла; четыре одинаковых абзаца — уже петля.
	reasoningLoopRepeats = 4
	// Проверка идёт не на каждый токен, а раз в столько байт прироста:
	// поиск по всему буферу на каждой дельте был бы квадратичным.
	reasoningLoopStride = 2048
)

// reasoningLoopGuard копит размышление одного круга и замечает петлю.
type reasoningLoopGuard struct {
	text      strings.Builder
	checkedAt int
}

func (g *reasoningLoopGuard) looped(delta string) bool {
	g.text.WriteString(delta)
	size := g.text.Len()
	if size-g.checkedAt < reasoningLoopStride || size < reasoningLoopFragment*reasoningLoopRepeats {
		return false
	}
	g.checkedAt = size
	text := g.text.String()
	fragment := text[size-reasoningLoopFragment:]
	if len(strings.TrimSpace(fragment)) < reasoningLoopFragment/2 {
		return false
	}
	return strings.Count(text, fragment) >= reasoningLoopRepeats
}

func (g *reasoningLoopGuard) size() int { return g.text.Len() }
