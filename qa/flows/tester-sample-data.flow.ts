import { readFileSync } from "node:fs";
import { authFile, env } from "../lib/env.ts";
import { expect, test } from "../lib/proof.ts";

// Use the isolated admin origin with locally provisioned admin permissions and MFA session fixtures.
test("add sample data to a dedicated Test workspace once", async ({ page, proof }) => {
  test.skip(process.env.QA_TESTER_SAMPLE_FIXTURES !== "1", "Prepare the localhost admin auth fixtures and set QA_TESTER_SAMPLE_FIXTURES=1.");
  const state = JSON.parse(readFileSync(authFile(), "utf8")) as {
    origins: { origin: string; localStorage: { name: string; value: string }[] }[];
  };
  const stored = state.origins.find((origin) => origin.origin === env.webURL)?.localStorage.find((item) => item.name === "auth_token");
  if (!stored) throw new Error("No local admin session prepared");
  const token = JSON.parse(stored.value) as { access_token: string };
  const headers = { Authorization: `Bearer ${token.access_token}` };
  await page.addInitScript(() => {
    const token = localStorage.getItem("auth_token");
    if (token) localStorage.setItem("warmbly_admin_token", token);
  });

  const email = `sample-proof-${Date.now()}@example.test`;
  const created = await page.request.post(`${env.apiURL}/admin/testers`, {
    headers,
    data: { email, org_name: "Sample review proof", reason: "Local sample-data proof", password_hours: 48 },
  });
  expect(created.status()).toBe(201);
  const { organization_id: orgID, user_id: userID } = await created.json() as { organization_id: string; user_id: string };
  try {
    await page.goto("/testers");
    const row = page.getByRole("listitem").filter({ has: page.getByText(email, { exact: true }) });
    await expect(row).toContainText("No sample data added");
    const add = row.getByRole("button", { name: `Add sample data for ${email}`, exact: true });
    await expect(add).toBeEnabled();
    await proof.chapter("Dedicated Test workspace", "Real localhost API. Admin permissions and verified session are local auth fixtures, not MFA proof.");
    await proof.shot("tester-sample-data-before", { caption: "A dedicated Test workspace can be populated without connecting a mailbox." });

    await add.click();
    const dialog = page.getByRole("dialog", { name: "Add sample data?", exact: true });
    await expect(dialog).toContainText("Six fictional, unsubscribed contacts");
    await expect(dialog).toContainText("One draft campaign");
    await expect(dialog).toContainText("No mailbox is created and no email is sent");
    await proof.shot("tester-sample-data-confirm", { caption: "Confirmation names the synthetic dataset, draft campaign and manual mailbox requirement." });
    const seeded = page.waitForResponse((response) => response.url().endsWith(`/admin/organizations/${orgID}/sample-data`) && response.request().method() === "POST");
    await dialog.getByRole("button", { name: "Add sample data", exact: true }).click();
    const response = await seeded;
    expect(response.status()).toBe(200);
    expect((await response.json()).created).toBe(true);
    await expect(dialog).toBeHidden();
    await expect(row).toContainText("Sample data added");
    await expect(add).toBeDisabled();
    await proof.shot("tester-sample-data-added", { caption: "The real API persists the dataset and audit timestamp; repeat addition is disabled." });

    const repeat = await page.request.post(`${env.apiURL}/admin/organizations/${orgID}/sample-data`, { headers });
    expect(repeat.status()).toBe(200);
    expect((await repeat.json()).created).toBe(false);
    await page.reload();
    await expect(row).toContainText("Sample data added");
    await expect(add).toBeDisabled();
  } finally {
    await page.request.delete(`${env.apiURL}/admin/testers/${userID}`, { headers });
  }
});
