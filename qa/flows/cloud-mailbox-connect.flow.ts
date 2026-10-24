import { expect, test } from "../lib/proof.ts";

// Synthetic Cloud responses keep this proof local; no Google grants or Cloud accounts are used.
test("existing Google and Outlook Cloud mailboxes connect in bulk with individual retries", async ({ page, proof }) => {
  const mailboxes = Array.from({ length: 28 }, (_, index) => ({
    id: `00000000-0000-4000-8000-${String(index + 1).padStart(12, "0")}`,
    email: `sender${String(index + 1).padStart(2, "0")}@${index === 27 ? "outlook.test" : "workspace.test"}`,
    name: `Sender ${index + 1}`, provider: index === 0 ? "smtp_imap" : index === 27 ? "outlook" : "gmail", status: "active",
    mail_host: index === 27 ? "microsoft365" : "google_workspace",
  }));
  const connected = new Set<string>();
  let interrupted = false;
  await page.route("**/v1/auth/me", async (route) => {
    const response = await route.fetch();
    const user = await response.json();
    await route.fulfill({ response, json: { ...user, admin_permissions: user.admin_permissions | (1 << 14), session_mfa_verified: true, product_tour_completed_at: "2026-01-01T00:00:00Z" } });
  });
  await page.route("**/v1/auth/config", async (route) => {
    const response = await route.fetch();
    await route.fulfill({ response, json: { ...await response.json(), self_hosted: true } });
  });
  await page.route("**/v1/cloud-link", (route) => route.fulfill({ json: {
    connected: true, reachable: true, default_cloud_url: "https://cloud.example.invalid",
    link: { instance_id: "fixture", organization_id: "fixture", organization_name: "Cloud workspace", cloud_url: "https://cloud.example.invalid", connected_at: "2026-01-01T00:00:00Z" },
  } }));
  await page.route("**/v1/cloud-link/mailboxes", (route) => route.fulfill({ json: { data: [] } }));
  await page.route("**/v1/cloud-link/workspace-mailboxes", (route) => route.fulfill({ json: { data: mailboxes.filter((mailbox) => !connected.has(mailbox.id)) } }));
  await page.route("**/v1/cloud-link/workspace-mailboxes/*/adopt", async (route) => {
    expect(route.request().method()).toBe("POST");
    const mailbox = mailboxes.find((item) => route.request().url().includes(item.id));
    if (!mailbox) throw new Error("Unknown fixture mailbox");
    if (mailbox.id === mailboxes[6].id && !interrupted) {
      interrupted = true;
      await route.fulfill({ status: 503, json: { error: "Unavailable", message: "Connection interrupted. Retry this mailbox." } });
      return;
    }
    connected.add(mailbox.id);
    await route.fulfill({ status: 201, json: mailbox });
  });

  await page.goto("/app/emails");
  await page.getByRole("button", { name: "Add account", exact: true }).first().click();
  const picker = page.getByRole("region", { name: "In your Warmbly Cloud workspace" });
  await expect(picker.getByRole("button", { name: "Connect all (28)" })).toBeVisible();
  await proof.chapter("Use the mailboxes already on Cloud", "Synthetic Google Workspace and Outlook mailboxes; the real picker needs no new app passwords.");
  await proof.shot("cloud-mailboxes-available", { caption: "Existing Cloud mailboxes appear before new connection methods, with search and one bulk action." });
  await picker.getByPlaceholder("Search Cloud mailboxes").fill("workspace.test");
  await expect(picker.getByRole("button", { name: "Connect matching (27)" })).toBeVisible();
  await proof.shot("cloud-mailboxes-search", { caption: "Search narrows the bulk action to the displayed Google Workspace mailboxes." });
  await picker.getByPlaceholder("Search Cloud mailboxes").fill("");
  await picker.getByRole("button", { name: "Connect all (28)" }).click();
  await expect(page.getByRole("alertdialog")).toContainText("Warmup and its history stay on Cloud");
  await proof.shot("cloud-mailboxes-confirm", { caption: "Confirmation explains Cloud history and the in-place reuse of local app-password mailboxes." });
  await page.getByRole("alertdialog").getByRole("button", { name: "Confirm", exact: true }).click();
  await expect(picker.getByText(/27 mailboxes connected/)).toBeVisible();
  await expect(picker.getByRole("alert")).toContainText("Connection interrupted");
  await proof.shot("cloud-mailboxes-partial", { caption: "One synthetic interruption leaves only that mailbox retryable; the other 27 stay connected." });
  await picker.getByRole("button", { name: `Connect ${mailboxes[6].email}`, exact: true }).click();
  await page.getByRole("alertdialog").getByRole("button", { name: "Confirm", exact: true }).click();
  await expect(picker.getByText(/28 mailboxes connected/)).toBeVisible();
  expect(connected.size).toBe(28);
  await proof.shot("cloud-mailboxes-connected", { caption: "All 28 connected without recreating Cloud mailboxes or restarting their history. Cloud responses are fixtures." });
});
