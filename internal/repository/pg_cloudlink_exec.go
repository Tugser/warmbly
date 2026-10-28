package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/warmbly/warmbly/internal/models"
)

// TaskTypeCloudWarmup is a Cloud warmup send this instance executes for a
// mailbox it keeps the only credential to.
const TaskTypeCloudWarmup = "cloud_warmup"

// CloudCommand is a command leased from Warmbly Cloud as stored here. Payload
// is the PoolLinkCommand body sealed with the workspace key.
type CloudCommand struct {
	ID             uuid.UUID
	InstanceID     uuid.UUID
	EmailAccountID uuid.UUID
	Kind           string
	Payload        string
	Actions        []string
	MessageHandle  *uuid.UUID
	RFCMessageID   string
	NotAfter       time.Time
	State          string
	TaskID         *uuid.UUID
	Attempts       int
	// Admitted is set once the worker was allowed to run an action command.
	Admitted bool
}

// CloudReport is one undelivered answer or observation owed to Cloud.
type CloudReport struct {
	ID             uuid.UUID
	InstanceID     uuid.UUID
	EmailAccountID *uuid.UUID
	Kind           string
	CommandID      *uuid.UUID
	RemoteID       *uuid.UUID
	Body           json.RawMessage
	Attempts       int
}

// CloudWarmupMessage is warmup mail reported to Cloud, by the handle Cloud echoes back.
type CloudWarmupMessage struct {
	Handle         uuid.UUID
	EmailAccountID uuid.UUID
	UserID         uuid.UUID
	InternalID     uuid.UUID
	RFCMessageID   string
	GmailID        string
	UID            uint32
	UIDValidity    uint32
	FolderPath     string
	Folder         string
	ReportedAt     *time.Time
}

// CloudSendReservation is the outbound reservation behind a Cloud send command.
type CloudSendReservation struct {
	Worker   *uuid.UUID
	Nonce    *uuid.UUID
	Started  bool
	Released bool
}

// CloudCommandTask is the local send task behind a Cloud send command.
type CloudCommandTask struct {
	Nonce    *uuid.UUID
	Status   string
	Released bool
}

// CloudCommandCounts is what the dashboard shows per instance-executed mailbox.
type CloudCommandCounts struct {
	Pending int
	Failed  int
}

type CloudLinkExecRepository interface {
	SetExecutor(ctx context.Context, accountID uuid.UUID, executor models.PoolLinkExecutor) error
	MailboxByRemote(ctx context.Context, instanceID, remoteID uuid.UUID) (*models.CloudLinkMailbox, error)
	HasInstanceExecuted(ctx context.Context, instanceID uuid.UUID) (bool, error)

	// StoreCommand keeps a leased command once; a replayed lease changes nothing.
	StoreCommand(ctx context.Context, c CloudCommand) (bool, error)
	// ClaimCommands leases due pending commands to this process for lease.
	ClaimCommands(ctx context.Context, limit int, lease time.Duration) ([]CloudCommand, error)
	// PrepareSendTask creates (once) the local task behind a send command.
	PrepareSendTask(ctx context.Context, commandID, accountID uuid.UUID, messageID string) (*CloudCommandTask, error)
	CancelSendTask(ctx context.Context, taskID uuid.UUID) error
	// SendReservation is the send behind a command, nil before it has a task.
	SendReservation(ctx context.Context, taskID uuid.UUID) (*CloudSendReservation, error)
	MarkDispatched(ctx context.Context, commandID uuid.UUID, admitUntil time.Time) error
	// BeginAction opens an action command's admission window before it is published; false when it already was.
	BeginAction(ctx context.Context, commandID uuid.UUID, admitUntil time.Time) (bool, error)
	// RevertAction closes the window again after a failed publish the worker never acted on.
	RevertAction(ctx context.Context, commandID uuid.UUID) error
	// AdmittedActions are dispatched actions the worker accepted that no worker event answers.
	AdmittedActions(ctx context.Context, limit int) ([]CloudCommand, error)
	RetryCommand(ctx context.Context, commandID uuid.UUID, at time.Time, reason string) error
	// AnswerCommand records the outcome and queues it for Cloud, once.
	AnswerCommand(ctx context.Context, commandID uuid.UUID, result models.PoolLinkCommandResult) error
	CommandForTask(ctx context.Context, taskID uuid.UUID) (*CloudCommand, error)
	DispatchedAction(ctx context.Context, accountID uuid.UUID, action string, handle *uuid.UUID, rfcMessageID string) (*CloudCommand, error)
	// AbandonedCommands are dispatched commands whose local outcome is settled without an answer.
	AbandonedCommands(ctx context.Context, grace time.Duration, limit int) ([]CloudCommand, error)
	CommandCounts(ctx context.Context, orgID uuid.UUID) (map[uuid.UUID]CloudCommandCounts, error)

	QueueObservation(ctx context.Context, instanceID, accountID, remoteID uuid.UUID, obs models.PoolLinkWarmupObservation) error
	ClaimReports(ctx context.Context, limit int, lease time.Duration) ([]CloudReport, error)
	ReportDelivered(ctx context.Context, id uuid.UUID) error
	ReportFailed(ctx context.Context, id uuid.UUID, at time.Time, reason string) error

	UpsertWarmupMessage(ctx context.Context, m CloudWarmupMessage) (uuid.UUID, error)
	MarkWarmupMessageReported(ctx context.Context, handle uuid.UUID) error
	ForgetWarmupMessage(ctx context.Context, handle uuid.UUID) error
	WarmupMessageByHandle(ctx context.Context, accountID, handle uuid.UUID) (*CloudWarmupMessage, error)
	ReportedWarmupMessage(ctx context.Context, accountID, internalID uuid.UUID) (*CloudWarmupMessage, error)
	// RecentOwnAction reports whether this instance ran one of actions on the message lately.
	RecentOwnAction(ctx context.Context, accountID, handle uuid.UUID, actions []string, within time.Duration) (bool, error)
	// IsOwnCloudSend reports whether a message is this mailbox's own copy of a Cloud warmup send.
	IsOwnCloudSend(ctx context.Context, accountID uuid.UUID, messageID string) (bool, error)
	Prune(ctx context.Context, answeredFor, messagesFor time.Duration) error
}

type cloudLinkExecRepository struct{ db *pgxpool.Pool }

func NewCloudLinkExecRepository(db *pgxpool.Pool) CloudLinkExecRepository {
	return &cloudLinkExecRepository{db: db}
}

func (r *cloudLinkExecRepository) SetExecutor(ctx context.Context, accountID uuid.UUID, executor models.PoolLinkExecutor) error {
	if !executor.Valid() {
		return errors.New("unknown executor")
	}
	_, err := r.db.Exec(ctx, `UPDATE cloud_link_mailboxes SET executor = $2 WHERE email_account_id = $1`, accountID, string(executor.OrCloud()))
	return err
}

func (r *cloudLinkExecRepository) MailboxByRemote(ctx context.Context, instanceID, remoteID uuid.UUID) (*models.CloudLinkMailbox, error) {
	m, err := scanCloudLinkMailbox(r.db.QueryRow(ctx, `SELECT `+cloudLinkMailboxColumns+` FROM cloud_link_mailboxes WHERE instance_id = $1 AND remote_id = $2`, instanceID, remoteID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return m, err
}

func (r *cloudLinkExecRepository) HasInstanceExecuted(ctx context.Context, instanceID uuid.UUID) (bool, error) {
	var ok bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM cloud_link_mailboxes WHERE instance_id = $1 AND executor = 'instance')
	 OR EXISTS(SELECT 1 FROM cloud_link_commands WHERE instance_id = $1 AND state <> 'answered')`, instanceID).Scan(&ok)
	return ok, err
}

func (r *cloudLinkExecRepository) StoreCommand(ctx context.Context, c CloudCommand) (bool, error) {
	if c.Actions == nil {
		c.Actions = []string{}
	}
	tag, err := r.db.Exec(ctx, `INSERT INTO cloud_link_commands (id, instance_id, email_account_id, kind, payload, actions, message_handle, rfc_message_id, not_after)
	 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) ON CONFLICT (id) DO NOTHING`,
		c.ID, c.InstanceID, c.EmailAccountID, c.Kind, c.Payload, c.Actions, c.MessageHandle, c.RFCMessageID, c.NotAfter)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

const cloudCommandColumns = `id, instance_id, email_account_id, kind, payload, actions, message_handle, rfc_message_id, not_after, state, task_id, attempts, admitted_at IS NOT NULL`

func scanCloudCommand(row pgx.Row) (*CloudCommand, error) {
	var c CloudCommand
	if err := row.Scan(&c.ID, &c.InstanceID, &c.EmailAccountID, &c.Kind, &c.Payload, &c.Actions, &c.MessageHandle, &c.RFCMessageID, &c.NotAfter, &c.State, &c.TaskID, &c.Attempts, &c.Admitted); err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *cloudLinkExecRepository) queryCommands(ctx context.Context, query string, args ...any) ([]CloudCommand, error) {
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CloudCommand
	for rows.Next() {
		c, err := scanCloudCommand(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func (r *cloudLinkExecRepository) ClaimCommands(ctx context.Context, limit int, lease time.Duration) ([]CloudCommand, error) {
	return r.queryCommands(ctx, `UPDATE cloud_link_commands SET next_attempt_at = NOW() + make_interval(secs => $2), attempts = attempts + 1
	 WHERE id IN (SELECT id FROM cloud_link_commands WHERE state = 'pending' AND next_attempt_at <= NOW()
	   ORDER BY next_attempt_at LIMIT $1 FOR UPDATE SKIP LOCKED)
	 RETURNING `+cloudCommandColumns, limit, lease.Seconds())
}

func (r *cloudLinkExecRepository) PrepareSendTask(ctx context.Context, commandID, accountID uuid.UUID, messageID string) (*CloudCommandTask, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	// The task id is the command id, so a re-claimed command finds the task it already made.
	if _, err = tx.Exec(ctx, `INSERT INTO tasks (id, task_type, email_account_id, status, message_id, scheduled_at, created_at, updated_at)
	 VALUES ($1, 'cloud_warmup', $2, 'active', $3, NOW(), NOW(), NOW()) ON CONFLICT (id) DO NOTHING`, commandID, accountID, messageID); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE cloud_link_commands SET task_id = $1, admit_until = not_after WHERE id = $1 AND state = 'pending'`, commandID); err != nil {
		return nil, err
	}
	out := &CloudCommandTask{}
	err = tx.QueryRow(ctx, `SELECT send_executor_nonce, status::text, send_released_at IS NOT NULL FROM tasks
	 WHERE id = $1 AND email_account_id = $2 AND task_type = 'cloud_warmup'`, commandID, accountID).Scan(&out.Nonce, &out.Status, &out.Released)
	if err != nil {
		return nil, err
	}
	return out, tx.Commit(ctx)
}

func (r *cloudLinkExecRepository) CancelSendTask(ctx context.Context, taskID uuid.UUID) error {
	_, err := r.db.Exec(ctx, `UPDATE tasks SET status = 'cancelled', updated_at = NOW()
	 WHERE id = $1 AND task_type = 'cloud_warmup' AND status = 'active' AND send_executor_nonce IS NULL`, taskID)
	return err
}

func (r *cloudLinkExecRepository) SendReservation(ctx context.Context, taskID uuid.UUID) (*CloudSendReservation, error) {
	var s CloudSendReservation
	err := r.db.QueryRow(ctx, `SELECT send_executor_worker, send_executor_nonce, send_executor_started_at IS NOT NULL, send_released_at IS NOT NULL
	 FROM tasks WHERE id = $1 AND task_type = 'cloud_warmup'`, taskID).Scan(&s.Worker, &s.Nonce, &s.Started, &s.Released)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *cloudLinkExecRepository) BeginAction(ctx context.Context, commandID uuid.UUID, admitUntil time.Time) (bool, error) {
	tag, err := r.db.Exec(ctx, `UPDATE cloud_link_commands SET state = 'dispatched', dispatched_at = NOW(), admit_until = $2, last_error = NULL
	 WHERE id = $1 AND state = 'pending' AND kind = 'action'`, commandID, admitUntil)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (r *cloudLinkExecRepository) RevertAction(ctx context.Context, commandID uuid.UUID) error {
	_, err := r.db.Exec(ctx, `UPDATE cloud_link_commands SET state = 'pending', dispatched_at = NULL, admit_until = NULL
	 WHERE id = $1 AND state = 'dispatched' AND kind = 'action' AND admitted_at IS NULL`, commandID)
	return err
}

func (r *cloudLinkExecRepository) AdmittedActions(ctx context.Context, limit int) ([]CloudCommand, error) {
	return r.queryCommands(ctx, `SELECT `+cloudCommandColumns+` FROM cloud_link_commands
	 WHERE state = 'dispatched' AND kind = 'action' AND admitted_at IS NOT NULL
	 AND NOT ('verify_removal' = ANY(actions)) AND NOT ('remove_from_spam' = ANY(actions) AND message_handle IS NOT NULL)
	 ORDER BY admitted_at LIMIT $1`, limit)
}

func (r *cloudLinkExecRepository) MarkDispatched(ctx context.Context, commandID uuid.UUID, admitUntil time.Time) error {
	_, err := r.db.Exec(ctx, `UPDATE cloud_link_commands SET state = 'dispatched', dispatched_at = COALESCE(dispatched_at, NOW()), admit_until = $2, last_error = NULL
	 WHERE id = $1 AND state = 'pending'`, commandID, admitUntil)
	return err
}

func (r *cloudLinkExecRepository) RetryCommand(ctx context.Context, commandID uuid.UUID, at time.Time, reason string) error {
	_, err := r.db.Exec(ctx, `UPDATE cloud_link_commands SET next_attempt_at = $2, last_error = left($3, 300) WHERE id = $1 AND state = 'pending'`, commandID, at, reason)
	return err
}

func (r *cloudLinkExecRepository) AnswerCommand(ctx context.Context, commandID uuid.UUID, result models.PoolLinkCommandResult) error {
	body, err := json.Marshal(result)
	if err != nil {
		return err
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var instanceID, accountID uuid.UUID
	err = tx.QueryRow(ctx, `UPDATE cloud_link_commands SET state = 'answered', outcome = $2, answered_at = NOW(), admit_until = NULL
	 WHERE id = $1 AND state <> 'answered' RETURNING instance_id, email_account_id`, commandID, result.Outcome).Scan(&instanceID, &accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO cloud_link_reports (instance_id, email_account_id, kind, command_id, body)
	 VALUES ($1, $2, 'result', $3, $4) ON CONFLICT (command_id) DO NOTHING`, instanceID, accountID, commandID, body); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *cloudLinkExecRepository) CommandForTask(ctx context.Context, taskID uuid.UUID) (*CloudCommand, error) {
	c, err := scanCloudCommand(r.db.QueryRow(ctx, `SELECT `+cloudCommandColumns+` FROM cloud_link_commands WHERE task_id = $1`, taskID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return c, err
}

func (r *cloudLinkExecRepository) DispatchedAction(ctx context.Context, accountID uuid.UUID, action string, handle *uuid.UUID, rfcMessageID string) (*CloudCommand, error) {
	c, err := scanCloudCommand(r.db.QueryRow(ctx, `SELECT `+cloudCommandColumns+` FROM cloud_link_commands
	 WHERE email_account_id = $1 AND kind = 'action' AND state = 'dispatched' AND $2 = ANY(actions)
	 AND (($3::uuid IS NOT NULL AND message_handle = $3) OR ($3::uuid IS NULL AND $4 <> '' AND (btrim(rfc_message_id, '<>') = btrim($4, '<>')
	   OR message_handle IN (SELECT handle FROM cloud_link_warmup_messages m WHERE m.email_account_id = $1 AND btrim(m.rfc_message_id, '<>') = btrim($4, '<>')))))
	 ORDER BY dispatched_at LIMIT 1`, accountID, action, handle, rfcMessageID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return c, err
}

func (r *cloudLinkExecRepository) AbandonedCommands(ctx context.Context, grace time.Duration, limit int) ([]CloudCommand, error) {
	// A send the worker began stays with the generic unknown-send path for a day before it is answered.
	return r.queryCommands(ctx, `SELECT `+cloudCommandColumns+` FROM cloud_link_commands k
	 WHERE k.state = 'dispatched' AND (
	   (k.kind = 'send' AND NOT EXISTS(SELECT 1 FROM tasks t WHERE t.id = k.task_id AND t.send_executor_started_at IS NOT NULL
	       AND t.send_released_at IS NULL AND k.not_after > NOW() - INTERVAL '1 day')
	     AND (k.not_after < NOW() OR EXISTS(SELECT 1 FROM tasks t WHERE t.id = k.task_id
	       AND (t.status = 'cancelled' OR (t.send_released_at IS NOT NULL AND t.send_result_applied_at IS NULL)))))
	   OR (k.kind = 'action' AND k.not_after < NOW() - make_interval(secs => $1)))
	 ORDER BY k.dispatched_at LIMIT $2`, grace.Seconds(), limit)
}

func (r *cloudLinkExecRepository) CommandCounts(ctx context.Context, orgID uuid.UUID) (map[uuid.UUID]CloudCommandCounts, error) {
	rows, err := r.db.Query(ctx, `SELECT k.email_account_id,
	 COUNT(*) FILTER (WHERE k.state <> 'answered'),
	 COUNT(*) FILTER (WHERE k.outcome IN ('failed', 'refused', 'expired') AND k.answered_at > NOW() - INTERVAL '24 hours')
	 FROM cloud_link_commands k JOIN email_accounts ea ON ea.id = k.email_account_id
	 WHERE ea.organization_id = $1 AND k.received_at > NOW() - INTERVAL '7 days'
	 GROUP BY k.email_account_id`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]CloudCommandCounts{}
	for rows.Next() {
		var id uuid.UUID
		var c CloudCommandCounts
		if err := rows.Scan(&id, &c.Pending, &c.Failed); err != nil {
			return nil, err
		}
		out[id] = c
	}
	return out, rows.Err()
}

func (r *cloudLinkExecRepository) QueueObservation(ctx context.Context, instanceID, accountID, remoteID uuid.UUID, obs models.PoolLinkWarmupObservation) error {
	body, err := json.Marshal(obs)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(ctx, `INSERT INTO cloud_link_reports (instance_id, email_account_id, kind, remote_id, body) VALUES ($1, $2, 'observation', $3, $4)`,
		instanceID, accountID, remoteID, body)
	return err
}

func (r *cloudLinkExecRepository) ClaimReports(ctx context.Context, limit int, lease time.Duration) ([]CloudReport, error) {
	rows, err := r.db.Query(ctx, `UPDATE cloud_link_reports SET next_attempt_at = NOW() + make_interval(secs => $2), attempts = attempts + 1
	 WHERE id IN (SELECT id FROM cloud_link_reports WHERE delivered_at IS NULL AND next_attempt_at <= NOW()
	   ORDER BY created_at LIMIT $1 FOR UPDATE SKIP LOCKED)
	 RETURNING id, instance_id, email_account_id, kind, command_id, remote_id, body, attempts`, limit, lease.Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CloudReport
	for rows.Next() {
		var c CloudReport
		if err := rows.Scan(&c.ID, &c.InstanceID, &c.EmailAccountID, &c.Kind, &c.CommandID, &c.RemoteID, &c.Body, &c.Attempts); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *cloudLinkExecRepository) ReportDelivered(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx, `UPDATE cloud_link_reports SET delivered_at = NOW(), last_error = NULL WHERE id = $1`, id)
	return err
}

func (r *cloudLinkExecRepository) ReportFailed(ctx context.Context, id uuid.UUID, at time.Time, reason string) error {
	_, err := r.db.Exec(ctx, `UPDATE cloud_link_reports SET next_attempt_at = $2, last_error = left($3, 300) WHERE id = $1 AND delivered_at IS NULL`, id, at, reason)
	return err
}

const cloudWarmupMessageColumns = `handle, email_account_id, user_id, internal_id, rfc_message_id, gmail_id, uid, uid_validity, folder_path, folder, reported_at`

func scanCloudWarmupMessage(row pgx.Row) (*CloudWarmupMessage, error) {
	var m CloudWarmupMessage
	var uid, validity int64
	err := row.Scan(&m.Handle, &m.EmailAccountID, &m.UserID, &m.InternalID, &m.RFCMessageID, &m.GmailID, &uid, &validity, &m.FolderPath, &m.Folder, &m.ReportedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	m.UID, m.UIDValidity = uint32(uid), uint32(validity)
	return &m, nil
}

func (r *cloudLinkExecRepository) UpsertWarmupMessage(ctx context.Context, m CloudWarmupMessage) (uuid.UUID, error) {
	var handle uuid.UUID
	// A reported message seen again after a move keeps the handle Cloud already holds.
	err := r.db.QueryRow(ctx, `UPDATE cloud_link_warmup_messages SET internal_id = $3, gmail_id = $4, uid = $5, uid_validity = $6, folder_path = $7, folder = $8
	 WHERE handle = (SELECT handle FROM cloud_link_warmup_messages WHERE email_account_id = $1 AND rfc_message_id = $2 AND rfc_message_id <> ''
	   AND reported_at IS NOT NULL ORDER BY created_at LIMIT 1)
	 AND NOT EXISTS(SELECT 1 FROM cloud_link_warmup_messages o WHERE o.email_account_id = $1 AND o.internal_id = $3)
	 RETURNING handle`, m.EmailAccountID, m.RFCMessageID, m.InternalID, m.GmailID, int64(m.UID), int64(m.UIDValidity), m.FolderPath, m.Folder).Scan(&handle)
	if err == nil {
		return handle, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, err
	}
	err = r.db.QueryRow(ctx, `INSERT INTO cloud_link_warmup_messages (handle, email_account_id, user_id, internal_id, rfc_message_id, gmail_id, uid, uid_validity, folder_path, folder)
	 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	 ON CONFLICT (email_account_id, internal_id) DO UPDATE SET gmail_id = EXCLUDED.gmail_id, uid = EXCLUDED.uid,
	   uid_validity = EXCLUDED.uid_validity, folder_path = EXCLUDED.folder_path, folder = EXCLUDED.folder
	 RETURNING handle`, m.Handle, m.EmailAccountID, m.UserID, m.InternalID, m.RFCMessageID, m.GmailID, int64(m.UID), int64(m.UIDValidity), m.FolderPath, m.Folder).Scan(&handle)
	return handle, err
}

func (r *cloudLinkExecRepository) MarkWarmupMessageReported(ctx context.Context, handle uuid.UUID) error {
	_, err := r.db.Exec(ctx, `UPDATE cloud_link_warmup_messages SET reported_at = COALESCE(reported_at, NOW()) WHERE handle = $1`, handle)
	return err
}

func (r *cloudLinkExecRepository) ForgetWarmupMessage(ctx context.Context, handle uuid.UUID) error {
	_, err := r.db.Exec(ctx, `DELETE FROM cloud_link_warmup_messages WHERE handle = $1 AND reported_at IS NULL`, handle)
	return err
}

func (r *cloudLinkExecRepository) WarmupMessageByHandle(ctx context.Context, accountID, handle uuid.UUID) (*CloudWarmupMessage, error) {
	return scanCloudWarmupMessage(r.db.QueryRow(ctx, `SELECT `+cloudWarmupMessageColumns+` FROM cloud_link_warmup_messages
	 WHERE handle = $1 AND email_account_id = $2 AND reported_at IS NOT NULL`, handle, accountID))
}

func (r *cloudLinkExecRepository) ReportedWarmupMessage(ctx context.Context, accountID, internalID uuid.UUID) (*CloudWarmupMessage, error) {
	return scanCloudWarmupMessage(r.db.QueryRow(ctx, `SELECT `+cloudWarmupMessageColumns+` FROM cloud_link_warmup_messages
	 WHERE email_account_id = $1 AND internal_id = $2 AND reported_at IS NOT NULL`, accountID, internalID))
}

func (r *cloudLinkExecRepository) RecentOwnAction(ctx context.Context, accountID, handle uuid.UUID, actions []string, within time.Duration) (bool, error) {
	var ok bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM cloud_link_commands WHERE email_account_id = $1 AND message_handle = $2
	 AND dispatched_at > NOW() - make_interval(secs => $3) AND actions && $4::text[])`, accountID, handle, within.Seconds(), actions).Scan(&ok)
	return ok, err
}

func (r *cloudLinkExecRepository) IsOwnCloudSend(ctx context.Context, accountID uuid.UUID, messageID string) (bool, error) {
	if messageID == "" {
		return false, nil
	}
	var ok bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE email_account_id = $1 AND task_type = 'cloud_warmup'
	 AND btrim(message_id, '<>') = btrim($2, '<>'))`, accountID, messageID).Scan(&ok)
	return ok, err
}

func (r *cloudLinkExecRepository) Prune(ctx context.Context, answeredFor, messagesFor time.Duration) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM cloud_link_reports WHERE delivered_at < NOW() - make_interval(secs => $1)`, answeredFor.Seconds()); err != nil {
		return err
	}
	// A handle goes once Cloud has had the message deleted, once the mailbox no longer runs here, or past any retention Cloud can set.
	if _, err := r.db.Exec(ctx, `DELETE FROM cloud_link_warmup_messages m WHERE
	   EXISTS(SELECT 1 FROM cloud_link_commands k WHERE k.message_handle = m.handle AND k.email_account_id = m.email_account_id
	     AND 'delete' = ANY(k.actions) AND k.outcome = 'done' AND k.answered_at < NOW() - INTERVAL '1 day')
	   OR (m.created_at < NOW() - INTERVAL '7 days' AND NOT EXISTS(SELECT 1 FROM cloud_link_mailboxes c
	     WHERE c.email_account_id = m.email_account_id AND c.executor = 'instance'))
	   OR m.created_at < NOW() - make_interval(secs => $1)`, messagesFor.Seconds()); err != nil {
		return err
	}
	_, err := r.db.Exec(ctx, `DELETE FROM cloud_link_commands k WHERE k.state = 'answered' AND k.answered_at < NOW() - make_interval(secs => $1)
	 AND NOT EXISTS(SELECT 1 FROM cloud_link_reports r WHERE r.command_id = k.id AND r.delivered_at IS NULL)`, answeredFor.Seconds())
	return err
}
