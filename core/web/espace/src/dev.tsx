import { useState } from "react";
import { createAdapter } from "./a2ui/adapter";
import { galleryFixtures, sampleOrb, sampleProposals } from "./a2ui/fixtures";
import { ProposalsContext, type Proposals } from "./a2ui/proposals";
import { Surfaces } from "./a2ui/Surfaces";
import { setReads, type Boot } from "./data/api";
import { sample } from "./data/sample";
import { onBump, sampleOpen } from "./data/samplecore";
import { applyNodes, step, store } from "./data/store";
import { useWords, type Lang } from "./i18n";
import strings from "./strings.json";

// The dev server's workspace (npm run dev), when no eriphe-core is behind:
// a made-up graph whose pages a make-believe core reads (data/sample.ts,
// samplecore.ts), made-up proposals and orb, and ?gallery, every component
// of the catalog. main.tsx imports it only on the dev server: none of it is
// in the built app (a test checks espace.js holds no sample's words).

export type Dev = { proposals: Proposals; start(): () => void; gallery: boolean };

// devBoot is the boot eriphe-core would give, in the language asked
// (?lang=en), else the browser's.
export function devBoot(): Boot {
  const asked = new URLSearchParams(location.search).get("lang");
  const lang: Lang = asked === "en" || (!asked && !navigator.language.startsWith("fr")) ? "en" : "fr";
  return { lang, name: "Eriphe", csrf: "", strings: strings[lang], live: false };
}

export function dev(lang: Lang): Dev {
  setReads({
    page: async (id, version) => sample.page(id, version),
    trace: async (id) => sample.trace(id),
    file: async (id) => sample.file(id),
    open: (id, _block, _source, item) => sampleOpen(id, item),
  });
  return {
    proposals: sampleProposals(lang),
    gallery: new URLSearchParams(location.search).has("gallery"),
    start() {
      step(store, applyNodes, { reset: true, nodes: sample.nodes });
      store.setState({ orb: sampleOrb(lang) });
      // A view the make-believe core changed is read again, as eriphe-core's "doc" says.
      return onBump((id) => store.setState((s) => ({ docs: { ...s.docs, [id]: (s.docs[id] ?? 0) + 1 } })));
    },
  };
}

// Gallery shows every component of the catalog, one surface under the
// other, to look at them on the dev server (?gallery).
export function Gallery() {
  const { lang } = useWords();
  const [adapter] = useState(() => {
    const a = createAdapter(lang);
    a.applyMessages(galleryFixtures(lang));
    return a;
  });
  return (
    <ProposalsContext.Provider value={sampleProposals(lang)}>
      <div className="gallery">
        <Surfaces adapter={adapter} />
      </div>
    </ProposalsContext.Provider>
  );
}
