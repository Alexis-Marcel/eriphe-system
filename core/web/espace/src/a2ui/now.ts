import { useEffect, useState } from "react";

// useNow is the time it is now, again each minute: the one clock a page
// reads, for its header's time and an agenda's now line alike.
export function useNow(): Date {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const id = setInterval(() => setNow(new Date()), 60_000);
    return () => clearInterval(id);
  }, []);
  return now;
}
