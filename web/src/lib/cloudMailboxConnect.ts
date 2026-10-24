export const CLOUD_MAILBOX_PICKER_EVENT = "warmbly:connect-cloud-mailbox";
export const CLOUD_MAILBOX_PICKER_OPENED = "cloud_mailbox_picker_opened";

export function cloudMailboxPickerOpened(error: unknown): boolean {
    return (error as { code?: string } | null)?.code === CLOUD_MAILBOX_PICKER_OPENED;
}
