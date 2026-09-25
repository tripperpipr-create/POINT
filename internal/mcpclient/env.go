package mcpclient

import (
	"sort"
	"strings"
)

// childEnvironmentNames — что из окружения ядра доходит до MCP-сервера.
//
// Ядро наследует всё окружение хоста расширения: пути данных Point, токен API
// ядра, переменные VS Code и Electron. Чужой программе из этого нужно только
// найти себя (PATH), свой дом и кэш (npm, npx) и сеть (прокси, сертификаты).
// Остальное — не её дело, и в первую очередь POINT_API_TOKEN.
var childEnvironmentNames = map[string]bool{
	"PATH": true, "PATHEXT": true, "HOME": true, "USERPROFILE": true,
	"APPDATA": true, "LOCALAPPDATA": true, "TEMP": true, "TMP": true, "TMPDIR": true,
	"SYSTEMROOT": true, "WINDIR": true, "COMSPEC": true, "SYSTEMDRIVE": true,
	"PROGRAMFILES": true, "PROGRAMFILES(X86)": true, "PROGRAMDATA": true,
	"LANG": true, "LC_ALL": true, "LC_CTYPE": true,
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true,
	"NODE_EXTRA_CA_CERTS": true, "SSL_CERT_FILE": true, "SSL_CERT_DIR": true,
	"XDG_CONFIG_HOME": true, "XDG_CACHE_HOME": true, "XDG_DATA_HOME": true,
}

// ChildEnvironment собирает окружение процесса сервера: разрешённое из base
// плюс переменные самого сервера (открытые и секреты). Переменная сервера
// перекрывает унаследованную. Имена сравниваются без регистра: на Windows
// `Path` и `PATH` — одна переменная.
func ChildEnvironment(base []string, server map[string]string) []string {
	merged := map[string]string{}
	names := map[string]string{}
	for _, item := range base {
		name, value, ok := strings.Cut(item, "=")
		if !ok || name == "" || !childEnvironmentNames[strings.ToUpper(name)] {
			continue
		}
		key := strings.ToUpper(name)
		merged[key] = value
		names[key] = name
	}
	for name, value := range server {
		key := strings.ToUpper(name)
		merged[key] = value
		names[key] = name
	}
	out := make([]string, 0, len(merged))
	for key, value := range merged {
		out = append(out, names[key]+"="+value)
	}
	sort.Strings(out)
	return out
}
