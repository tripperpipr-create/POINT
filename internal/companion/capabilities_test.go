package companion

import (
	"strings"
	"testing"
)

func TestPointIDEFeatureReplyKnowsSSH(t *testing.T) {
	reply, ok := pointIDEFeatureReply("есть ли в point ide подключение по ssh?")
	if !ok {
		t.Fatal("Point IDE capability question was not recognized")
	}
	for _, want := range []string{"профили", "SSH-терминал", "удалённых каталогов", "предпросмотр"} {
		if !strings.Contains(reply, want) {
			t.Fatalf("SSH capability reply %q does not contain %q", reply, want)
		}
	}
}
