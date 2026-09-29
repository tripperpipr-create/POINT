package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"local-agent-workbench/internal/domain"
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

// Модель, которая сразу отказывает, — для тестов, которым прогоны агентов
// нужны лишь побочным эффектом запуска Flow.
//
// Такие тесты смотрели в ollama разработчика на 127.0.0.1:11434 и держались
// только на стороже пакета, который рубил подключение. Здесь отказ свой,
// мгновенный и не повторяемый (400), а пресет местный и бесплатный, как
// ollama, — бюджет квеста считается так же.
func useRefusingTestModel(t *testing.T, agents ...*domain.ProjectAgent) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"test model refuses every request"}}`))
	}))
	t.Cleanup(server.Close)
	for _, agent := range agents {
		agent.Provider, agent.ProviderPreset, agent.BaseURL = domain.ProviderOpenAI, "llama-cpp", server.URL
	}
}
