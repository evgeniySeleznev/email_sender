//go:build ignore
// +build ignore

package main

import (
	"crypto/tls"
	"fmt"
	"log"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

func main() {
	// Настройки из settings.ini
	imapHost := "imap.yandex.ru"
	imapPort := 993
	user := "evgenii.seleznev.holo@yandex.ru"
	password := "jraysqvkashfnkaa"

	addr := fmt.Sprintf("%s:%d", imapHost, imapPort)
	log.Printf("Подключение к IMAP: %s", addr)

	start := time.Now()

	// Подключение через TLS
	c, err := client.DialTLS(addr, &tls.Config{
		ServerName:         imapHost,
		InsecureSkipVerify: false,
	})
	if err != nil {
		log.Fatalf("Ошибка подключения: %v", err)
	}
	defer c.Logout()

	log.Printf("Подключено за %v", time.Since(start))

	// Аутентификация
	start = time.Now()
	if err := c.Login(user, password); err != nil {
		log.Fatalf("Ошибка аутентификации: %v", err)
	}
	log.Printf("Аутентификация за %v", time.Since(start))

	// Получение списка папок
	start = time.Now()
	mailboxes := make(chan *imap.MailboxInfo, 10)
	done := make(chan error, 1)
	go func() {
		done <- c.List("", "*", mailboxes)
	}()

	log.Println("Список папок:")
	for m := range mailboxes {
		log.Printf("  - %s", m.Name)
	}

	if err := <-done; err != nil {
		log.Fatalf("Ошибка получения списка папок: %v", err)
	}
	log.Printf("Список папок за %v", time.Since(start))

	// Выбор INBOX
	start = time.Now()
	mbox, err := c.Select("INBOX", false)
	if err != nil {
		log.Fatalf("Ошибка выбора INBOX: %v", err)
	}
	log.Printf("INBOX выбран за %v, писем в папке: %d", time.Since(start), mbox.Messages)

	// Попробуем получить заголовки последних 5 писем
	if mbox.Messages > 0 {
		start = time.Now()
		from := uint32(1)
		to := mbox.Messages
		if mbox.Messages > 5 {
			from = mbox.Messages - 4
		}

		seqSet := new(imap.SeqSet)
		seqSet.AddRange(from, to)

		messages := make(chan *imap.Message, 10)
		done := make(chan error, 1)
		go func() {
			done <- c.Fetch(seqSet, []imap.FetchItem{imap.FetchEnvelope}, messages)
		}()

		log.Println("Последние письма:")
		for msg := range messages {
			if msg.Envelope != nil {
				from := ""
				if len(msg.Envelope.From) > 0 {
					from = msg.Envelope.From[0].Address()
				}
				log.Printf("  - От: %s, Тема: %s", from, msg.Envelope.Subject)
			}
		}

		if err := <-done; err != nil {
			log.Fatalf("Ошибка получения писем: %v", err)
		}
		log.Printf("Получение писем за %v", time.Since(start))
	}

	log.Println("IMAP тест успешно завершен!")
}
