# Shape your workspace by talking

The workspace is the screen Eriphe composes for you from the tools you linked: your calendars, your
mail, your notes, your projects, whatever they are. There is no fixed dashboard and no widget to
configure. You say what you want to see, Eriphe puts it there, and it keeps it up to date. This
guide explains how, for someone who runs Eriphe but does not write code.

The examples use invented tools and a user called Sam. Yours will differ: Eriphe knows no tool in
advance and works with the ones you linked.

## What you see

It is Eriphe's home page: open Eriphe's address and it is there. From the conversation, the
**Composed workspace** button at the top, or Eriphe's name, brings you back to it.

A grid of **tiles**, Eriphe calls them surfaces, each showing one thing: today's appointments,
mails to answer, a project's next step, an error rate as a chart. Above the grid, a strip of
**chips**: what is there but folded, out of the way. Next to them, a narrow column with one line
from Eriphe about the moment and a field to write to it.

On a computer the screen never scrolls. Eriphe chooses what is in front and how big: what waits for your answer
first, then what matters now, the rest folded. When nothing needs you, it says so in a sentence
rather than showing an empty grid.

Who writes the tiles:

- **Eriphe**, when you ask it to show something;
- **a skill**, the things Eriphe does on its own at set times or on an event (a morning
  preparation, say), each keeping its own tiles up to date;
- **a work session**, while it runs, with its progress;
- **Eriphe itself, for what waits for you**: a proposal to approve, an alert, a message held for
  later. These come and go with what they show.

### Let an AI arrange it

Choosing what is in front is done by the **workspace composer**, a small AI turn each time
something changes. It is **off by default**: turn it on in Settings → Agents → Workspace composer
("Compose with an AI") and pick its agent (a fast model is enough). Off, nothing is sent to an AI
for this: tiles are simply ordered by importance, and everything else in this guide still works
except that your general preferences ("always show…", "less of…") are not taken into account.

## Arrival

When you open the workspace in the morning, your day is already there. Eriphe brings a skill,
**arrivee**, that prepares the workspace at 06:00 and again when you come back after three hours or
more away. It looks at what you linked and writes a few tiles: the day's appointments, the messages
that need an answer, projects with their deadline and next task, accounts if a tool for them is
linked. Nothing useful for one of these, no tile: three right tiles are better than eight empty
ones.

If a tile could be better with a tool the skill is not allowed to read, it adds a small "prepare
more" tile with a button. The button asks Eriphe to propose a new version of the skill with that
tool; you say yes or no on its card.

The arrival skill is yours like any other: see it in Settings → Skills, change it ("prepare
my arrival at 7 instead"), turn it off or remove it.

## Ask for something to be shown

Write to Eriphe, in the workspace's column or in the conversation:

> Show me this week's unread mails.

A tile appears at once marked **In preparation**, then fills. There are two kinds of tile:

- **Live**: the tile reads a tool directly, and Eriphe's core reads it again by itself, every few
  minutes, when the tool says something changed, and when you open the tile. No AI is involved
  after the tile is written, so it costs nothing to keep fresh. Unread mails, appointments, a
  balance, a counter are live.
- **A summary, written once**: something that needs understanding ("summarize what happened on the
  backup server this week", "which of these mails need an answer from me") is written by an
  assistant, often as a background task, and shows the values as they were when it was written,
  with their date. It does not change on its own; ask again, or make it recurring (below).

A tile you ask for once lasts about a day, then goes. Titles say what you see ("Unread this week",
"To answer"), never the name of the tool behind it.

A live tile only reads what you let Eriphe read freely: a tool whose reading needs your approval
each time, or that you blocked, cannot feed a live tile. Eriphe tells you, and Settings → Surfaces
shows what each tile reads and its last error; change the tool's access if you want it, or ask for
a summary instead.

## Make it come back

Say it with a time or an event:

> Every morning, show me the deliveries expected today.
>
> Every Monday, a summary of last week's invoices.

Eriphe writes a **skill** for it: a short set of instructions with its trigger, which rewrites the
same tile each time. You see it on a card in the conversation, in plain words, and **nothing runs
before you say yes**. Then it is in Settings → Skills with its runs; to change it or stop it, ask
("show the deliveries at 8 instead", "stop the invoices summary") or do it there.

## Shape it by talking

Say what you want, about the whole workspace or one tile:

> Make the calendar big.
>
> Put the deliveries top right.
>
> Always put backup failures in front.
>
> No more newsletters from news.example.org.
>
> Show me less of this.

Two kinds of request:

- **Now**: "make the calendar big", "put this top right" arrange the screen for this session, as if
  you had done it by hand.
- **In general**: "always…", "never…", "less of…" are **preferences**. Eriphe remembers them as
  memories about you, and the composer reads them every time it arranges the workspace. You manage
  them like any memory: ask Eriphe what it remembers, or to forget one. There is no settings page
  for the workspace and no rule to maintain.

Eriphe also notices what you do. If you keep closing the same kind of tile, it may propose a
preference ("Sam does not want … in front"); it waits for your yes like any memory it infers.

When you write while a tile is open, your message is about that tile: "this", "less of this",
"look into it" refer to it.

## Shape it by hand

Each tile has three small buttons in its corner, also reachable with the keyboard:

- the **grip** moves it (drag it to a cell, or use the arrow keys);
- the **size** button changes its size (a click goes up, then back to the smallest; + or −);
- the **cross** closes it **for the session**: it goes to the end of the chip strip, marked
  "closed for the session", and is not put back in front unless you open it or ask.

What you moved or resized stays where you put it, at that size, for the session; the rest is
arranged around it. A session lasts while you are active, with no break of more than an hour, and
ends with the day (a late night after midnight counts as the same day). The next session starts
fresh, with your preferences.

## Focus on one thing

Click a tile or a chip to **open** it. The workspace recomposes around it: the tile large, what
relates to it around, the rest folded. What relates is found from what the tiles show, never from a tool's name: the same source,
the same work session or skill, the same day. Opening a tile also reads its data again. Eriphe may add one line above about what matters
there, or say nothing.

**Back to the overview** (or Escape) restores the workspace as it was. The focus also ends by
itself after 15 minutes.

## Buttons and proposals

- **A button asks Eriphe.** Clicking it sends the words written on it as your message, as if you
  had typed them ("Draft a reply", "Look into this error"). Eriphe answers in the conversation and
  does what you would have asked. No button writes, sends or deletes anything by itself.
- **A proposal runs only after your yes.** When Eriphe proposes something (an event to add, a
  reply to send, a new skill), it shows as a card with **Yes** and **No**, in the conversation and,
  where it fits, in the tile it concerns. A proposed appointment shows on the day's tile as a
  ghost marked **Proposed**, with what it overlaps. Answering in one place answers everywhere.
- **Content from outside cannot act.** A tile that shows content from outside (a mail, a web
  page, a message from someone else) is treated as data: it carries no button written after that
  content was read, and a proposal shown there has no Yes / No but says "answer it in the
  conversation". The tiles Eriphe keeps for what waits for you are the exception: their Yes / No
  stay, with the warning "Comes from external content: check what is about to be done." A ghost on
  a day reads "Proposed · external content", and Settings → Surfaces tags such tiles external. A
  mail that says "click here to approve" cannot make anything happen.

## A new tool, no code

Link a tool (Settings → Tools: a calendar, a mail account, a home automation, any MCP server) and
it is usable in the workspace at once: ask to see something from it. Eriphe looks at what the tool
can read and the shape of its answers, and builds the tile from a fixed set of shapes (text,
tables, metrics, charts, timelines, messages, a day's agenda). Nothing in Eriphe is written for a
specific tool.

## With nothing linked

The workspace still works: it shows what waits for you (proposals, alerts, sessions) and Eriphe's
line. The arrival skill writes one tile saying what the workspace could show in the morning once
you link appointments, a mailbox or notes. Link one and ask.

## On a phone

On a narrow screen the page scrolls, in one column: Eriphe's line and the field to write to it
first, then the tiles one under the other, most important first (the order Eriphe chose, not their
place on a computer's grid), then the folded chips at the end. A tile keeps only its cross: to make
one bigger or move it, say so. Open a tile and it takes the whole screen, with **Back to the
overview** and the field to write about it at the top.

## Where the conversation is

The conversation with Eriphe stays, with its history, the tasks and Settings, at
`/conversation`: **Open the conversation**, in the workspace's column, goes there. What you write
in the workspace's column is a message of that conversation, and Eriphe's answer comes there; the
column only says it was sent.

If the workspace cannot open on a device (an old browser, a script blocked, Eriphe out of reach
for a while), the page offers the conversation instead, and **Always open the conversation here**.
That choice is per browser, in Settings → My workspace → Home page (Réglages → Mon espace → Page
d'accueil), where you can give the workspace back its place. While Eriphe has no AI set up yet, its
address opens the conversation, which says how to set one up.

## Privacy and safety

- **Images go through your Eriphe.** The page never loads an address from outside: an image is
  fetched by Eriphe's core, only from a public address, without your cookies or credentials, and
  only an image or a video. An address an assistant wrote after reading outside content is
  dropped.
- **Strict page rules.** The workspace runs under a strict content security policy: no script or
  style from a tile, no inline code. Links in a tile open in a new tab, only on your click, and
  only to web or mail addresses.
- **Outside content is data.** Whatever a mail or a page says, Eriphe reads it as information,
  never as instructions; tiles that show it cannot trigger actions (above).
- **Tiles read only what you allow.** A live tile reads a tool only if its access lets Eriphe read
  it freely; a skill reads only the tools you approved on its card. Settings → Surfaces lists every
  tile with who wrote it, what it reads and when it last changed; remove any from there.

Technical details (surfaces, sources, authors, the composer, the layout) are in the
[core README](../core/README.md#workspace-web).
