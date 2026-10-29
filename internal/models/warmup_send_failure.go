package models

import "time"

const (
	MailboxNotLoadedPrefix   = "The sending worker has not loaded this mailbox yet"
	MailboxLoadingCode       = "MAILBOX_LOADING"
	WarmupFailureLoading     = "mailbox_loading"
	WarmupLoadingGracePeriod = time.Hour
)
