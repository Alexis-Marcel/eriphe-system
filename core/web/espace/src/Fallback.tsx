import { Component, type ReactNode } from "react";
import { useWords } from "./i18n";

// The workspace's fallback: when it cannot open, the user gets the
// conversation, always there at /conversation, and can make it this
// browser's home page (POST /settings/home, web_pages.go). The shell
// (web/templates/espace.html) carries the same before the app runs, for a
// bundle that does not load or a boot that fails (main.tsx).

export const CONVO = "/conversation";

export function Fallback({ csrf }: { csrf: string }) {
  const { t } = useWords();
  return (
    <div className="espace-loading failed" role="alert">
      <div className="espace-fallback">
        <p>{t("espace.fallback")}</p>
        <p><a href={CONVO}>{t("espace.convo")}</a></p>
        {csrf && (
          <form method="post" action="/settings/home">
            <input type="hidden" name="csrf" value={csrf} />
            <input type="hidden" name="home" value="conversation" />
            <button type="submit" className="linkish">{t("espace.home.convo")}</button>
          </form>
        )}
      </div>
    </div>
  );
}

type Props = { csrf: string; children: ReactNode };

// FailSafe shows the fallback when the workspace throws while it draws,
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
    return this.state.failed ? <Fallback csrf={this.props.csrf} /> : this.props.children;
  }
}
