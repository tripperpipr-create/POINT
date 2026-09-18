package textutil

import (
	"strings"
	"unicode"
)

// Совпадение слов — общее правило для мастера и компаньона.
//
// Оба подбирают персонажа под задачу и оба показывают человеку, почему подобрали
// именно этого. Правило у них было своё: у мастера — со списком служебных слов,
// у компаньона — без него вовсе. Компаньон считал предлог совпадением и накидывал
// за «на» те же очки, что за «биллинг», а его проверка «в сообщении меньше трёх
// значимых слов» считала значимыми «а что по этому». Одно и то же обоснование,
// показанное человеку, не может зависеть от того, кто его показывает.

// Предлог найдётся в любом описании, поэтому «совпало: на» — не причина взять
// агента в отряд, а шум, выданный за причину. Однорунные предлоги отсекает
// длина, двухбуквенные её проходят — и всплывают именно там, где показанное
// обоснование и должно было сделать подбор проверяемым, а не авторитетным.
// Оценке они мешают тем же: +12 всякому, у кого в описании есть «на».
var stopTokens = func() map[string]bool {
	set := map[string]bool{}
	for _, word := range strings.Fields(`
		на по из от до за для как что это этот эта эти же ли не ни но или об обо со во при про над под без
		через чтобы если когда где кто чем уже ещё еще его её ее их он она оно они мы вы ты все всё весь
		так там тут был была были быть есть нет да мне меня нас вам том тем тот та то бы
		the and for with that this from into not are was were has have had can could should would will
		its it is of in on to at by or an as be but if then than when where who what which
		all any our your you we they them their
	`) {
		set[word] = true
	}
	return set
}()

// Tokens — значимые слова текста: без служебных, без повторов, без односимвольных.
func Tokens(value string) []string {
	fields := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	// Повтор слова в задаче — не двойное совпадение: «тесты, тесты и ещё раз
	// тесты» поднимали агента втрое против того, кто подходит не хуже.
	seen := make(map[string]bool, len(fields))
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if len([]rune(field)) < 2 || stopTokens[field] || seen[field] {
			continue
		}
		seen[field] = true
		out = append(out, field)
	}
	return out
}

// Overlap — сколько значимых слов слева встретилось справа.
func Overlap(left, right []string) int {
	if len(left) == 0 || len(right) == 0 {
		return 0
	}
	set := make(map[string]bool, len(right))
	for _, token := range right {
		set[token] = true
	}
	overlap := 0
	for _, token := range left {
		if set[token] {
			overlap++
		}
	}
	return overlap
}

// IsStopWord сообщает, служебное ли слово. Нужен там, где текст уже разобран на
// слова чужим кодом и фильтр приходится применять отдельно.
func IsStopWord(word string) bool { return stopTokens[strings.ToLower(word)] }
