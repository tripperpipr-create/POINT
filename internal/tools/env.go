package tools

import (
	"os"
	"strings"
)

// sanitizedProcessEnv returns a deny-by-default environment for custom tools.
// Host secrets (API keys, tokens, cloud credentials) are not inherited.
func sanitizedProcessEnv() []string {
	allowExact := map[string]struct{}{
		"PATH": {}, "PATHEXT": {}, "SYSTEMROOT": {}, "COMSPEC": {},
		"TEMP": {}, "TMP": {}, "TMPDIR": {},
		"HOME": {}, "USERPROFILE": {}, "HOMEDRIVE": {}, "HOMEPATH": {},
		"USERNAME": {}, "USER": {}, "LOGNAME": {}, "LANG": {}, "LC_ALL": {},
		"TZ": {}, "TERM": {}, "SHELL": {}, "PWD": {},
		"GOROOT": {}, "GOPATH": {}, "GOBIN": {}, "GOMODCACHE": {}, "GOCACHE": {},
		"GOPROXY": {}, "GOSUMDB": {}, "GO111MODULE": {}, "GOTOOLCHAIN": {},
		"NODE_PATH": {}, "NPM_CONFIG_PREFIX": {},
		"APPDATA": {}, "LOCALAPPDATA": {}, "ALLUSERSPROFILE": {}, "PUBLIC": {},
		"NUMBER_OF_PROCESSORS": {}, "OS": {}, "WINDIR": {},
	}
	allowPrefix := []string{
		"GO", "NODE_", "NPM_CONFIG_", "JAVA_HOME", "JDK_", "PYTHON", "VIRTUAL_ENV",
		"CARGO_", "RUSTUP_", "DOTNET_", "MSYSTEM", "MINGW", "PROCESSOR_",
		"PROGRAMFILES", "PROGRAMDATA",
	}
	env := os.Environ()
	out := make([]string, 0, len(env)/4)
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if key == "" {
			continue
		}
		upper := strings.ToUpper(key)
		if strings.Contains(upper, "KEY") || strings.Contains(upper, "TOKEN") || strings.Contains(upper, "SECRET") || strings.Contains(upper, "PASSWORD") || strings.Contains(upper, "CREDENTIAL") {
			continue
		}
		if _, ok := allowExact[upper]; ok {
			out = append(out, entry)
			continue
		}
		allowed := false
		for _, prefix := range allowPrefix {
			if strings.HasPrefix(upper, strings.ToUpper(prefix)) {
				allowed = true
				break
			}
		}
		if allowed {
			out = append(out, entry)
		}
	}
	return out
}

// hostProcessEnv — окружение команды на устройстве человека. Сверх общего
// списка ему нужен свой SSH (ключ через агент, plink), иначе `git pull` по ssh
// падает там, где у человека в терминале работает; и git не должен ждать
// пароля с клавиатуры, которой у процесса нет.
func hostProcessEnv() []string {
	out := sanitizedProcessEnv()
	for _, key := range []string{"GIT_SSH", "GIT_SSH_COMMAND", "GIT_SSH_VARIANT", "SSH_AUTH_SOCK"} {
		if value, ok := os.LookupEnv(key); ok {
			out = append(out, key+"="+value)
		}
	}
	return append(out, "GIT_TERMINAL_PROMPT=0")
}
