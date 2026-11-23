import { describe, expect, it } from "vitest";
import { since, stamp } from "./format";

// stamp dates what happened in the browser's time: built from local
// fields here, so that the test holds in any zone.
const local = (y: number, mo: number, d: number, h: number, mi: number) => new Date(y, mo - 1, d, h, mi).toISOString();
const now = new Date(2026, 10, 19, 15, 0);

describe("stamp", () => {
  it("says today's time alone", () => {
    expect(stamp("fr", local(2026, 11, 19, 13, 13), now)).toBe("13 h 13");
    expect(stamp("en", local(2026, 11, 19, 13, 13), now)).toBe("1:13 pm");
    expect(stamp("fr", local(2026, 11, 19, 9, 5), now)).toBe("9 h 05");
  });

  it("says yesterday with its time", () => {
    expect(stamp("fr", local(2026, 11, 18, 13, 13), now)).toBe("hier, 13 h 13");
    expect(stamp("en", local(2026, 11, 18, 13, 13), now)).toBe("yesterday, 1:13 pm");
  });

  it("says the day this year, with the year before", () => {
    expect(stamp("fr", local(2026, 3, 4, 13, 13), now)).toBe("4 mars");
    expect(stamp("en", local(2026, 11, 2, 13, 13), now)).toBe("Nov 2");
    expect(stamp("fr", local(2025, 11, 19, 13, 13), now)).toBe("19 nov. 2025");
    expect(stamp("en", local(2025, 11, 19, 13, 13), now)).toBe("Nov 19, 2025");
  });

  it("says nothing of what is no date", () => {
    expect(stamp("fr", undefined, now)).toBe("");
    expect(stamp("fr", "soon", now)).toBe("");
  });
});

describe("since", () => {
  it("says how long ago within the hour, never a second clock", () => {
    expect(since("fr", local(2026, 11, 19, 14, 30), now)).toMatch(/^il y a\s30\smin$/);
    expect(since("en", local(2026, 11, 19, 14, 30), now)).toMatch(/^30\smin\.? ago$/);
    expect(since("fr", local(2026, 11, 19, 15, 0), now)).toBe("maintenant");
  });

  it("is the stamp past the hour, or ahead of now", () => {
    expect(since("fr", local(2026, 11, 19, 13, 13), now)).toBe("13 h 13");
    expect(since("fr", local(2026, 11, 19, 15, 10), now)).toBe("15 h 10");
    expect(since("fr", undefined, now)).toBe("");
  });
});
