package workspace

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestExtractSymbolsKnowsDeclarationsOfEachLanguage(t *testing.T) {
	cases := []struct {
		language string
		source   string
		want     []string
	}{
		{"Go", "func (s *Server) Handle(w http.ResponseWriter) {\nfunc New() *Server {\ntype Server struct {\nfunc (r repo[T]) List() {\n\tmux.HandleFunc(\"GET /api/documents\", s.list)\n", []string{"Server.Handle", "Handle", "New", "Server", "repo.List", "List", "/api/documents"}},
		{"PHP", "final class DocumentsController extends Controller\n    public static function listDocuments(Request $r)\n    private function &ref()\nRoute::get('/api/documents', [DocumentsController::class, 'list']);\n    #[Route('/api/flags', methods: ['GET'])]\n", []string{"DocumentsController", "listDocuments", "ref", "/api/documents", "/api/flags"}},
		{"PHP", "$arUrlRewrite = [\n  ['CONDITION' => '#^/api/documents/#', 'RULE' => '', 'PATH' => '/api/index.php'],\n", []string{"/api/documents/"}},
		{"TypeScript", "export default async function loadUser(id: string) {\nexport const fetchFlags = async (project: string): Promise<Flag[]> => {\nexport abstract class FlagService {\n  async getAll(project: string): Promise<Flag[]> {\n  if (x) {\n@Controller('flag')\n  @Get('get-flag')\nrouter.get('/api/documents', handler)\nexport interface Flag {\n", []string{"loadUser", "fetchFlags", "FlagService", "getAll", "flag", "get-flag", "/api/documents", "Flag"}},
		{"Python", "async def fetch(url):\nclass Client:\n    def close(self):\n", []string{"fetch", "Client", "close"}},
		{"Rust", "fn main() {\nstruct X {}\n", nil},
	}
	for _, item := range cases {
		got := extractSymbols(item.language, scanLines(item.source))
		if !reflect.DeepEqual(got, item.want) {
			t.Errorf("%s: %q, ждали %q", item.language, got, item.want)
		}
	}
}

// 03.10: search_code по пути API и по имени PHP-метода ничего не находил —
// метод и маршрут не были символами. Теперь они первые в выдаче.
func TestSearchCodeFindsRoutesAndMethods(t *testing.T) {
	root := t.TempDir()
	write := func(name, text string) {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("local/routes.php", "<?php\nRoute::get('/api/documents', [DocumentsController::class, 'index']);\n")
	write("local/DocumentsController.php", "<?php\nclass DocumentsController {\n    public function index() { return Documents::all(); }\n    protected function prepareRows(array $rows) { return $rows; }\n}\n")
	write("docs/notes.md", "API documents are documented elsewhere.\n")
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fs.BuildIndex(context.Background()); err != nil {
		t.Fatal(err)
	}
	for query, want := range map[string]string{"/api/documents": "local/routes.php", "prepareRows": "local/DocumentsController.php"} {
		result, err := fs.SearchContext(context.Background(), query, 3, 4096)
		if err != nil || len(result.Chunks) == 0 || result.Chunks[0].Path != want {
			t.Fatalf("%s: %+v %v", query, result.Chunks, err)
		}
		hits := fs.LookupIndex(query, 3).Hits
		if len(hits) == 0 || hits[0].Path != want || hits[0].Kind != "symbol" {
			t.Fatalf("%s: навигация %+v", query, hits)
		}
	}
}
