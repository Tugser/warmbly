import { expect, test } from "../lib/proof.ts";

// Requires ../fixtures/campaign-estimate.sql applied to this worktree's rich-seed database.
const FIXTURES = process.env.QA_CAMPAIGN_ESTIMATE_FIXTURES === "1";

test.describe("campaign estimate", () => {
  test.skip(!FIXTURES, "Apply qa/fixtures/campaign-estimate.sql to this stack's database and set QA_CAMPAIGN_ESTIMATE_FIXTURES=1.");

  test("review a campaign's finish date, what sets its pace and what would finish it sooner", async ({ page, proof }) => {
    await page.goto("/app/campaigns");
    await page.getByRole("button", { name: "New campaign" }).first().click();
    const dialog = page.getByRole("dialog", { name: "New campaign" });
    await expect(dialog).toBeVisible();
    await proof.chapter("Campaign estimate", "A three-email sequence to 300 leads from four warming mailboxes");

    await dialog.getByText("Agencies", { exact: true }).click();
    await dialog.getByRole("button", { name: /^Continue/ }).click();

    await expect(dialog.getByText("What will they receive?")).toBeVisible();
    await dialog.getByRole("textbox").first().fill("There's room to grow");
    // The placeholder lets clicks through to the editor beneath it.
    await dialog.getByText("Hi {{.FirstName}}, …", { exact: true }).click({ force: true });
    await page.keyboard.type("A quick idea for your agency.");
    await dialog.getByRole("button", { name: /Add the usual two follow-ups/ }).click();
    for (const n of ["2", "3"]) {
      // The email strip sits below the step tabs, so its chip is the last match.
      await dialog.getByRole("button", { name: new RegExp(`^${n} `) }).last().click();
      await dialog.getByText(/^Just bumping this in case it slipped past/).click({ force: true });
      await page.keyboard.type("Just bumping this in case it slipped past.");
    }
    await dialog.getByRole("button", { name: /^Continue/ }).click();
    await dialog.getByRole("button", { name: /^Continue/ }).click();

    await expect(dialog.getByText("Review and launch")).toBeVisible();
    await expect(dialog.getByText("Paced by replies")).toBeVisible();
    await expect(dialog.getByText("To finish sooner")).toBeVisible();
    await proof.dwell();
    await proof.shot("estimate-review", { caption: "The finish date with its arithmetic, what sets the pace, and the changes that would finish sooner" });

    const chart = dialog.getByRole("img", { name: /Projected sends, one bar per day/ });
    await expect(chart).toBeVisible();
    const box = await chart.boundingBox();
    if (box) await page.mouse.move(box.x + 4, box.y + box.height - 4);
    await expect(dialog.getByText(/^Day 1, /)).toBeVisible();
    await proof.shot("estimate-day-one", { caption: "One bar per day: day 1 is first emails only, with the warmup and the total per mailbox" });

    await page.mouse.move(0, 0);
    const last = dialog.getByText(/warmup goes back to its own schedule/);
    await last.scrollIntoViewIfNeeded();
    await expect(dialog.getByText("Each mailbox, per sending day")).toBeVisible();
    await expect(last).toBeVisible();
    await proof.dwell();
    await proof.shot("estimate-mailbox-mix", { caption: "What each mailbox sends with and without the campaign, and what happens to its warmup" });
  });
});
