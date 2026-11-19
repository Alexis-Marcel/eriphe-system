import { Component, type ReactNode } from "react";
import { useWords } from "./i18n";

// The workspace's error state: when it cannot open, it says so, with the
// way to try again. The shell (web/templates/espace.html) carries the same
// before the app runs, for a bundle that does not load or a boot that
// fails (main.tsx).

export function Fallback() {
  const { t } = useWords();
  return (
    <div className="espace-loading failed" role="alert">
      <div className="espace-fallback">
        <p>{t("espace.fallback")}</p>
        <p>
          <a href="/espace">{t("espace.retry")}</a>
        </p>
      </div>
    </div>
  );
}

type Props = { children: ReactNode };

// FailSafe shows the error state when the workspace throws while it draws,
// in place of a blank page.
export class FailSafe extends Component<Props, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError() {
    return { failed: true };
  }
  componentDidCatch(err: unknown) {
    console.error("espace:", err);
  }
  render() {
    return this.state.failed ? <Fallback /> : this.props.children;
  }
}
