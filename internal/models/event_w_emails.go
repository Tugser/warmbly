package models

import "github.com/google/uuid"

type JobEventWarmupFiled struct {
	EmailID  uuid.UUID `json:"email_id" avro:"email_id"`
	FilingID uuid.UUID `json:"filing_id" avro:"filing_id"`
}

// JobEventWarmupRescued is what became of a spam rescue on the receiving
// mailbox. InternalID is the received message the rescue was for.
type JobEventWarmupRescued struct {
	UserID     uuid.UUID `json:"user_id" avro:"user_id"`
	EmailID    uuid.UUID `json:"email_id" avro:"email_id"`
	InternalID uuid.UUID `json:"internal_id" avro:"internal_id"`
	Outcome    string    `json:"outcome" avro:"outcome"`
}

type JobEventNewEmail struct {
	UserID  uuid.UUID              `json:"user_id" avro:"user_id"`
	Message *EmailMessageStoreData `json:"message" avro:"message"`
	// ReportOriginalMessageID is set when this arrival is a delivery-status
	// notification or an abuse report ABOUT one of our own sends, and carries
	// that send's RFC Message-ID. The worker is the only side that can read it
	// (the id lives in the report's body, which the consumer has no access to),
	// and the consumer needs it to tell a report about a campaign send, which
	// the customer should see, from one about a warmup send, which is pool
	// traffic they never sent and cannot act on. Empty on ordinary mail and on
	// events from workers predating the field.
	ReportOriginalMessageID string `json:"report_original_message_id,omitempty" avro:"report_original_message_id"`
}

type JobEventRemoveEmail struct {
	UserID  uuid.UUID `json:"user_id" avro:"user_id"`
	EmailID uuid.UUID `json:"email_id" avro:"email_id"`
	ID      uuid.UUID `json:"id" avro:"id"`
	// SkippedFolder is set when the message was found in a folder the owner
	// excluded from sync: it still exists in the mailbox, so the removal is
	// filing, not deletion.
	SkippedFolder string `json:"skipped_folder,omitempty" avro:"skipped_folder"`
}

// JobEventWarmupRemovalChecked is where the worker found a warmup message
// after the sync reported it removed.
type JobEventWarmupRemovalChecked struct {
	UserID       uuid.UUID `json:"user_id" avro:"user_id"`
	EmailID      uuid.UUID `json:"email_id" avro:"email_id"`
	RFCMessageID string    `json:"rfc_message_id" avro:"rfc_message_id"`
	Outcome      string    `json:"outcome" avro:"outcome"`
	Recheck      bool      `json:"recheck,omitempty" avro:"recheck"`
}

type JobEventFlags struct {
	UserID  uuid.UUID `json:"user_id" avro:"user_id"`
	EmailID uuid.UUID `json:"email_id" avro:"email_id"`
	ID      uuid.UUID `json:"id" avro:"id"`
	Flags   []string  `json:"flags" avro:"flags"`
}

// JobEventFolderUpdate reports the canonical folder the provider now has a
// message in. The consumer resolves it against provider_folder, so local filing
// survives unless the provider itself moved the message.
type JobEventFolderUpdate struct {
	UserID  uuid.UUID `json:"user_id" avro:"user_id"`
	EmailID uuid.UUID `json:"email_id" avro:"email_id"`
	ID      uuid.UUID `json:"id" avro:"id"`
	Folder  string    `json:"folder" avro:"folder"`
	// Relayed answers a MESSAGE_FOLDER: the provider has the message in Folder
	// because Warmbly put it there, so only provider_folder follows.
	Relayed bool `json:"relayed,omitempty" avro:"relayed"`
	// Where the move left the message, when its handle changed with it: the
	// new Graph id, or the IMAP folder, UIDVALIDITY and UID.
	ProviderID string `json:"provider_id,omitempty" avro:"provider_id"`
	FolderPath string `json:"folder_path,omitempty" avro:"folder_path"`
	Mailbox    uint32 `json:"mailbox,omitempty" avro:"mailbox"`
	UID        uint32 `json:"uid,omitempty" avro:"uid"`
}

type JobEventEmailUpdate struct {
	UserID  uuid.UUID `json:"user_id" avro:"user_id"`
	EmailID uuid.UUID `json:"email_id" avro:"email_id"`
	ID      uuid.UUID `json:"id" avro:"id"`
	UID     uint32    `json:"uid" avro:"uid"`
	// int64 for the reason on JobEventHistoryIDUpdate.HistoryID. RFC 7162
	// caps a MODSEQ at 2^63-1, so the narrower type cannot lose one.
	ModSeq uint64 `json:"mod_seq" avro:"mod_seq"`
	// Mailbox is the folder's UIDVALIDITY, the generation UID belongs to.
	Mailbox uint32 `json:"mailbox" avro:"mailbox"`
	// FolderPath is the folder's name, its identity. Empty on events from
	// workers predating the field (the consumer then keeps the stored value).
	FolderPath string `json:"folder_path,omitempty" avro:"folder_path"`
	// Folder is the canonical folder the message now sits in; empty on events
	// from workers predating folder tracking (the consumer then keeps the
	// stored value).
	Folder string   `json:"folder,omitempty" avro:"folder"`
	Flags  []string `json:"flags" avro:"flags"`
}
