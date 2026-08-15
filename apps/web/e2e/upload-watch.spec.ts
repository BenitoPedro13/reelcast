import path from "node:path";
import { test, expect } from "@playwright/test";

const API_URL = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:3001";
const FIXTURE = path.join(__dirname, "fixtures", "sample.mp4");

test("upload through the UI, reach ready, and play with a parsed rendition", async ({
  page,
}) => {
  await page.goto("/upload");

  await page.getByLabel("Title").fill("Playwright e2e clip");
  await page.getByLabel("Video file").setInputFiles(FIXTURE);
  await page.getByRole("button", { name: "Upload", exact: true }).click();

  // Status stepper appears (post XHR-PUT + complete) and reaches `ready` via
  // 2s polling — no page reload, just the client-side redirect below.
  const stepper = page.getByTestId("status-stepper");
  await expect(stepper).toBeVisible({ timeout: 15_000 });
  await expect(stepper).toHaveAttribute("data-status", "ready", {
    timeout: 120_000, // real ffmpeg transcode + S3 upload + Postgres write
  });

  await page.waitForURL(/\/watch\//, { timeout: 5_000 });
  const videoId = page.url().split("/watch/")[1];

  // The <video> element actually advances past 0.
  const video = page.locator("video");
  await expect(video).toBeVisible();
  await video.evaluate((el: HTMLVideoElement) => {
    el.muted = true;
    return el.play();
  });
  await page.waitForTimeout(2000);
  const currentTime = await video.evaluate(
    (el: HTMLVideoElement) => el.currentTime,
  );
  expect(currentTime).toBeGreaterThan(0);

  // The player's parsed level matches a renditions row from the API.
  const activeRendition = page.getByTestId("active-rendition");
  await expect(activeRendition).not.toHaveText("Loading…", {
    timeout: 10_000,
  });
  const activeText = await activeRendition.textContent();
  const activeHeight = Number(activeText?.match(/(\d+)p/)?.[1]);
  expect(activeHeight).toBeGreaterThan(0);

  const res = await fetch(`${API_URL}/videos/${videoId}`);
  const body = (await res.json()) as { renditions: { height: number }[] };
  const heights = body.renditions.map((r) => r.height);
  expect(heights).toContain(activeHeight);
});
