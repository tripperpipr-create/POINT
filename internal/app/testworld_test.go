package app

import (
	"context"
	"testing"
)

// Подготовка мира для теста: одно приложение на временном каталоге.
//
// Шесть строк — очистка REDIS_ADDR, New(t.TempDir()), проверка ошибки и
// отложенный Shutdown — повторялись 141 раз в 36 файлах пакета. Копии
// расходились не формулировкой, а поведением: забытый Shutdown оставляет
// горутины прогона жить до конца пакета, и соседний тест падает на чужой
// работе. Уборка через t.Cleanup срабатывает и тогда, когда тест упал на
// t.Fatal в середине.
func newTestApp(t *testing.T) *App {
	t.Helper()
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	return application
}
