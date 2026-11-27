import { describe, expect, it } from "vitest";
import { clock, pickType, refused, TYPES, withWords } from "./dictate";

describe("dictation", () => {
  it("records what the browser makes, WebM first, MP4 on Safari", () => {
    expect(pickType(() => true)).toBe(TYPES[0]);
    expect(pickType((t) => t === "audio/mp4")).toBe("audio/mp4"); // Safari
    expect(pickType(() => false)).toBe(""); // the browser chooses
  });

  it("adds the words after what is typed", () => {
    expect(withWords("", " Bonjour. ")).toBe("Bonjour.");
    expect(withWords("Rappelle-moi", "demain à 9 h")).toBe("Rappelle-moi demain à 9 h");
    expect(withWords("Note :  ", "pain")).toBe("Note : pain");
    expect(withWords("Gardé", "   ")).toBe("Gardé");
  });

  it("tells a refused microphone from a missing one", () => {
    expect(refused({ name: "NotAllowedError" })).toBe(true);
    expect(refused({ name: "SecurityError" })).toBe(true);
    expect(refused({ name: "NotFoundError" })).toBe(false);
    expect(refused(null)).toBe(false);
  });

  it("shows the time spoken", () => {
    expect(clock(0)).toBe("0:00");
    expect(clock(65)).toBe("1:05");
  });
});
