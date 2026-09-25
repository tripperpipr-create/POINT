package mcpclient

import (
	"bufio"
	"bytes"
	"io"
)

// readSSE читает поток Server-Sent Events и отдаёт данные каждого события.
// each возвращает true, когда ждать больше нечего (пришёл наш ответ).
//
// Поля id, event и retry не нужны: возобновление потока Point не использует,
// а ответ опознаётся по id JSON-RPC внутри данных.
func readSSE(body io.Reader, limit int, each func(data []byte) (bool, error)) error {
	// Общий потолок на поток: одна строка без перевода или бесконечный поток
	// событий не должны съесть память ядра.
	reader := bufio.NewReaderSize(io.LimitReader(body, int64(limit)*4), 64<<10)
	var data bytes.Buffer
	flush := func() (bool, error) {
		if data.Len() == 0 {
			return false, nil
		}
		payload := append([]byte(nil), data.Bytes()...)
		data.Reset()
		return each(payload)
	}
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			line = bytes.TrimRight(line, "\r\n")
			switch {
			case len(line) == 0:
				done, handleErr := flush()
				if handleErr != nil || done {
					return handleErr
				}
			case line[0] == ':':
				// Комментарий — сервер держит соединение живым.
			case bytes.HasPrefix(line, []byte("data:")):
				chunk := bytes.TrimPrefix(bytes.TrimPrefix(line, []byte("data:")), []byte(" "))
				if data.Len() > 0 {
					data.WriteByte('\n')
				}
				data.Write(chunk)
				if data.Len() > limit {
					return newError(KindTooLarge, nil, "event is larger than %d bytes", limit)
				}
			}
		}
		if err == io.EOF {
			_, handleErr := flush()
			return handleErr
		}
		if err != nil {
			return newError(KindNetwork, err, "event stream broke: %s", err.Error())
		}
	}
}
