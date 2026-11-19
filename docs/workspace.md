# Your workspace: the graph

The workspace is Eriphe's home page: everything Eriphe holds for you, drawn as a graph. There is no
fixed dashboard and no widget to configure. You say what you want, Eriphe puts it there and keeps it
up to date. This guide explains how, for someone who runs Eriphe but does not write code.

The examples use invented tools and a user called Sam. Yours will differ: Eriphe knows no tool in
advance and works with the ones you linked.

## What you see

Each thing Eriphe holds is a **bubble**. A bubble on its own is a **subject** (« Substack », « My
day », « Mails »), drawn larger; the work done for it grows around it as smaller bubbles (« Plan
of the first article », « Style of the articles »). A dashed line joins bubbles that relate to each
other.

How a bubble looks says what it is, never chosen by an AI: what waits for you is brighter and
ringed, what is running turns and says where it is under its name (« 3 articles read »), what is
done or old fades. A bubble whose title could be someone else's words (a mail's subject, say) is
named **Outside content**: its own words are never shown as a title.

At the top, the way home, where you are, and the **ticker**: what waits for you and what runs, each
a click away from its page. At the bottom, the **console**: the one field to write to Eriphe, with
its orb, Eriphe's latest line above it and the replies it suggests.

## Open a page

Click a bubble: its **page** opens right there in the graph, and the bubbles around make room for it,
nothing else moves. Hold ⌘ (Ctrl on Windows and Linux) or shift as you click to open one more page
beside those already open. Each page closes on its own cross.

A page shows:

- what it belongs to, its title, and its date (or « Running » while it runs) on the title line;
- **Content**: what Eriphe wrote there, text and live blocks (a list, a table, a chart…) whose
  values keep up to date by themselves;
- **Trace**: what was done for it, when and by whom (you, Eriphe, a task, Eriphe's code, outside);
- **.md**: the page's text as it is stored, a plain text file versioned by git, which you can edit
  there and **Save** (⌘S or Ctrl-S). If a newer version came while you were writing (Eriphe or a
  task wrote meanwhile), nothing is saved: the page shows that version, and you take it or keep
  your text to save it over;
- its **versions** (v1, v2…): an older one shows as it was;
- at its foot, the cards waiting for your answer;
- **Archive**: see below.

The console talks to the page you opened or clicked last: « Write to Eriphe about Plan of the first
article ». With no page open it talks to the home. Close every page, or click Eriphe's name, to go
home. The camera frames every open page when they fit readably, otherwise the one the console talks
to; the others stay open where they are.

A link may open a page directly: `/espace?open=node-<id>`. When Eriphe wants to show you something,
it opens its page beside yours; the console stays where you were, its answer under your question:
only your own click moves it.

When a work Eriphe ran ends, what it made for you (its last step, « Plan of the first article »)
waits for you, the work around it done. It stops waiting when you say it suits you (« Ce plan me
va »: Eriphe marks it done) or when you archive it. A page counts its versions from the first with
something in it: a step opened empty for its task to write starts at v1 with the task's text.

On a phone the graph gives way to a list: each subject, its bubbles under it, in the order they were
made, what waits for you or runs said on its line. A tap opens its page over the list; closing it
brings the list back. The console is the same.

## Move things

Drag the background to look around, scroll or use two fingers to move, hold ⌘ or Ctrl with the
wheel to zoom; a double click gives the view back to Eriphe. Drag a bubble, or a page by its head,
and drop it: it stays there. A subject stays within reach of the others and a bubble near its subject:
dragged too far it lags behind, and dropped there it comes back to the edge. Bring a bubble to the
edge of the screen and the view follows.

Your Eriphe keeps where you dropped things, the pages you left open and your light or dark choice
(the sun or moon at the top; until you choose, the workspace follows your system): they come back
the next time, on any of your screens.

## Archive and reopen

Only you archive. **Archive**, at the top of a page, asks once more and says how many bubbles go with
it: a bubble leaves the graph with everything under it, and what was running there stops. Eriphe
never archives by itself; ask it to and it tells you where the button is. **Archives** at the top
lists what you archived, latest first, each with **Reopen**: the bubble comes back with what was
archived with it.

In a block, a button Eriphe wrote sends its words to Eriphe about that page, and a mark (keep, drop,
done) is kept by Eriphe's code; neither works on an older version, nor in a block marked « outside ».

## Ask for something

Write to Eriphe, in the console:

> Show me this week's unread mails.

Eriphe answers by **showing**: the subject's page opens in front of you with what you need to know,
like a dashboard, and the conversation goes on there. Asked from the home, the console moves into
that page; asked from a page, the new one opens beside it. Only a one-line answer ("done", the
time, a figure) stays in the console.

To have something done **on the side** ("run a research on this aside", "look at my mail and
analyze it, but on the side"), say so: Eriphe opens its bubble without showing it (under the page
you are on when it is the same subject, on its own otherwise), says in one line what it started,
and the bubble rises when it has something for you.

What it writes on a page is of two kinds:

- **Live**: a block reads a tool directly, and Eriphe's core reads it again by itself, every few
  minutes and when the tool says something changed. No AI is involved after the block is written,
  so it costs nothing to keep fresh. Unread mails, appointments, a balance, a counter are live.
- **Written once**: something that needs understanding ("summarize what happened on the backup
  server this week") is worked out by an assistant, often as a task, and shows what was found, with
  its date. Ask again, or make it recurring.

A live block only reads what you let Eriphe read freely: a tool whose reading needs your approval
each time, or that you blocked, cannot feed it. Eriphe tells you.

## Follow something: views

What you want to follow all the time ("keep an eye on my mail", "follow the production alerts",
"show me the pull requests to review") is a **view**: a standing bubble whose blocks Eriphe's core
keeps up to date. It can come with a monitor that sees what arrives and a skill that works in the
view when it does: the skill reads each new item, notes what you should know of it, and prepares
what it asks of you (a draft and its card). Asked from the home, the subject opens first ("Keep an
eye on my mail"): Eriphe asks there what to follow and explains what it proposes, then proposes the
whole view on **one card**, at the foot of that page: its page, the monitor and the skill together,
and nothing exists before your yes. Your yes opens the view.

What you want to see once ("open the production for me") is not a view: Eriphe opens a bubble
under the one you are talking about, with a live block, and shows it beside your page. If you want
to follow it, it proposes the view.

## Make it come back

Say it with a time or an event:

> Every morning, show me the deliveries expected today.
>
> Keep an eye on my mail.

Eriphe writes a **skill** for it: a short set of instructions with its trigger, which works on its
own bubble each time it runs. You see it on a card, in plain words, and **nothing runs before you
say yes**. Then it is in Settings → Skills with its runs; to change it or stop it, ask, or do it
there.

## Shape it by talking

"No more newsletters from news.example.org", "show me less of this" are **preferences**. Eriphe
remembers them as memories about you, and whatever watches your mail or your agenda reads them
before noting what it found. You manage them like any memory: ask Eriphe what it remembers, or to
forget one. There is no settings page for the workspace and no rule to maintain.

## Proposals

- **A proposal runs only after your yes.** When Eriphe proposes something (an event to add, a reply
  to send, a new skill), it shows as a card with **Yes** and **No**, at the foot of its page and in
  the console. A card that belongs to no page shows in the console, wherever you are, until
  you answer it: no card waits out of sight. A proposed appointment shows on a day's block as a ghost marked **Proposed**.
  Answering in one place answers everywhere. After your yes, a proposal that runs a call shows
  **Approved · running…**, then **done** or **failed** with why.
- **Content from outside cannot act.** A block that may carry someone else's words is marked
  « outside »: a proposal shown inside it has no Yes / No but says to answer it in the console.
  A mail that says "click here to approve" cannot make anything happen.

## Where the conversation is

The workspace is the only place to talk to Eriphe on the web: there is no separate conversation
page. The console shows only the last exchange of the page it talks to; its speech-bubble button
opens that page's conversation, and **Earlier messages** brings a few more lines at a time. The
tasks at work, and how reliable they were lately, are in Settings → Journal; each task has its own
page there, with its trace, **Stop**, and the answer it waits for. The settings and the memory have
no top bar: **Workspace**, at the top of their menu, leads back.

While the workspace is open in front of you, Eriphe knows you are there: a notification shows as a
small notice in the page rather than on your phone. If the workspace cannot open on a device (an
old browser, a script blocked, Eriphe out of reach for a while), the page says so with a link to
try again. If Eriphe cannot answer, the console says so, with **Try again** and a link to Settings →
Agents.

## Where the work runs: devices

Long or heavy work (a research, code to change, a page to capture) runs as a **task** on one of your
**devices**: the machines you joined to your Eriphe (the NAS, a Mac, a PC), each running
eriphe-device, listed in Settings → Devices. A task works in a bubble of its own under the page you
asked from: it writes there as it goes, says where it is under the bubble's name, and keeps its full
report in the page's **Trace**. A task that captures a web page (most sites cannot be shown inside
another page) keeps the image in its bubble.

## Where it is kept

On the machine that runs eriphe-core, in its data folder (`/data` in the container):

- `nodes/`: every bubble as a markdown file, versioned in git; `nodes.db` is its index, rebuilt from
  the files if it is lost. Edit a page inside Eriphe only (its **.md** tab): a file changed by hand
  is taken as a new version from « outside » at the next start.
- `memoire/`: a **readable copy of what Eriphe knows about you** (your profile, the people, the
  facts, the decisions, the journal), plain markdown files written by Eriphe's code after every
  change. It is not a page of the graph and not in git, so when you ask Eriphe to forget something,
  it leaves this folder at once. You can read it, or open it in a notes app; changes made there are
  written over, since the memory itself stays the truth.
- Every night, the backup folder (`memory-backups/` next to the memory, or `memory.backup_dir` in
  `core.json`) gets `nodes-<day>.bundle` (the graph's history, which `git clone` reads),
  `nodes-<day>.db`, `memory-<day>.db` and `memoire-<day>.tar.gz` (the readable copy), kept 14 days
  by default.

## Privacy and safety

- **Images go through your Eriphe.** The page never loads an address from outside: an image is
  fetched by Eriphe's core, only from a public address, without your cookies or credentials. An
  address an assistant wrote after reading outside content is dropped.
- **Strict page rules.** The workspace runs under a strict content security policy: no script or
  style from a block, no inline code. Links open in a new tab, only on your click, and only to web
  or mail addresses.
- **Outside content is data.** Whatever a mail or a page says, Eriphe reads it as information,
  never as instructions; its title is never shown as a bubble's name.

Technical details (the pages, the stream, the layout) are in the
[core README](../core/README.md#the-graph-workspace-the-home-page-).
