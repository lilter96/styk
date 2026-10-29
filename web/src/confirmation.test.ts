import { afterEach, expect, it, vi } from "vitest";
import { ApiError, createConfirmationIntent, sendConfirmation } from "./api";
afterEach(() => vi.restoreAllMocks());
it("reuses the same key and exact request after a lost response", async () => {
  const intent = createConfirmationIntent("a".repeat(32), "synthetic");
  const fetchMock = vi
    .spyOn(globalThis, "fetch")
    .mockRejectedValueOnce(new TypeError("network error"))
    .mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          mapping_id: 1,
          mapping_version: 1,
          review_id: intent.reviewId,
          evidence_class: "OPERATOR_ATTESTED",
          confirmed_at: "2026-01-01T00:00:00Z",
          idempotent_replay: true,
        }),
        { status: 200 },
      ),
    );
  await expect(sendConfirmation(intent)).rejects.toThrow("network error");
  expect((await sendConfirmation(intent)).idempotent_replay).toBe(true);
  expect(fetchMock.mock.calls[0][1]?.body).toBe(
    fetchMock.mock.calls[1][1]?.body,
  );
  expect(JSON.parse(intent.body).acknowledge).toBe(true);
  expect(Object.isFrozen(intent)).toBe(true);
});
it("preserves structured backend errors without exposing their payload", async () => {
  const intent = createConfirmationIntent("b".repeat(32), "");
  vi.spyOn(globalThis, "fetch").mockResolvedValue(
    new Response(
      JSON.stringify({
        error: "REVIEW_STALE",
        detail: "private database detail",
      }),
      { status: 409 },
    ),
  );
  try {
    await sendConfirmation(intent);
    throw new Error("expected failure");
  } catch (e) {
    expect(e).toBeInstanceOf(ApiError);
    expect((e as ApiError).code).toBe("REVIEW_STALE");
    expect((e as Error).message).not.toContain("private database detail");
  }
});
it("rejects malformed reviews and oversized explanations before creating an intent", () => {
  expect(() => createConfirmationIntent("invalid", "")).toThrow();
  expect(() =>
    createConfirmationIntent("a".repeat(32), "я".repeat(501)),
  ).toThrow();
});
