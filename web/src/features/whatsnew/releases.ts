/**
 * What changed in TeamUp, newest first, in words for the people who use it.
 *
 * Add an entry at the top with each update worth telling people about. Its id is
 * the day it went out (a second one the same day adds a suffix, 2026-11-12-b),
 * and a new id is what makes What's new open by itself, once, for everybody.
 */

export type Area = 'meetings' | 'chat' | 'tasks' | 'calendar' | 'code' | 'app';

export interface Change {
  area: Area;
  title: string;
  body: string;
  /** Where to try it, if there is one place. */
  link?: string;
}

export interface Release {
  id: string;
  /** The day it went out, as YYYY-MM-DD. */
  date: string;
  title: string;
  changes: Change[];
}

export const RELEASES: Release[] = [
  {
    id: '2026-11-12-c',
    date: '2026-11-12',
    title: 'A call window you can resize',
    changes: [
      {
        area: 'meetings',
        title: 'Make the minimised call bigger or smaller',
        body: 'Drag the arrow in the top left corner of a minimised call to resize it; the people in it grow and shrink with it, and it keeps that size next time on that device.',
      },
      {
        area: 'meetings',
        title: 'Move it with a finger',
        body: 'On a phone, put a finger anywhere on the minimised call and drag it where you want it. It stays on the screen, even when you turn the phone.',
      },
    ],
  },
  {
    id: '2026-11-12-b',
    date: '2026-11-12',
    title: 'Meeting chat in bubbles, and two fixes',
    changes: [
      {
        area: 'meetings',
        title: 'Every message in its own bubble',
        body: 'Messages in a meeting’s chat each sit in their own bubble with the time they were sent, so several in a row from one person are easy to tell apart. Yours are on the right.',
        link: '/calls',
      },
      {
        area: 'meetings',
        title: 'A phone stops ringing for good',
        body: 'When a caller hangs up just as your phone was about to ring again, it no longer starts ringing a moment later for a call that is over.',
      },
      {
        area: 'chat',
        title: 'Edit puts you straight in the box',
        body: 'Choosing Edit on one of your messages puts the cursor in the edit box, so you can start typing at once.',
      },
    ],
  },
  {
    id: '2026-11-12',
    date: '2026-11-12',
    title: 'Chat inside meetings, and tidier tasks',
    changes: [
      {
        area: 'meetings',
        title: 'Chat inside every meeting',
        body: 'The speech-bubble button in a call opens a chat for everyone in it, guests included. Somebody who joins late reads what came before, and a new message shows over the video when the chat is closed. Afterwards the chat stays in the call log, and the minutes include it.',
        link: '/calls',
      },
      {
        area: 'tasks',
        title: 'Labels you can make',
        body: 'Type a name in a task’s Labels card, such as bug or frontend, and press Enter. Labels your organization already has are offered as you type, and the Board can show only the tasks with one label.',
        link: '/tasks?tab=board',
      },
      {
        area: 'tasks',
        title: 'Long tasks stay short',
        body: 'A task’s history, comments, pull requests and commits show the latest few, with Show more for the rest.',
      },
      {
        area: 'app',
        title: 'What’s new',
        body: 'This page. It opens by itself once after each update, and you can find it again from your picture in the top corner.',
      },
    ],
  },
  {
    id: '2026-11-11',
    date: '2026-11-11',
    title: 'Task news, busy callers and meeting minutes',
    changes: [
      {
        area: 'tasks',
        title: 'Hear about your tasks',
        body: 'Whoever a task is assigned to, whoever made it and anybody added to it are told, with a sound, when it changes or somebody comments. Tasks shows a count, and each task card a dot, until you have looked.',
        link: '/tasks',
      },
      {
        area: 'calendar',
        title: 'Tasks on the calendar',
        body: 'Tasks with a due date show on that day in your calendar.',
        link: '/calendar',
      },
      {
        area: 'meetings',
        title: 'Calling somebody who is busy',
        body: 'Somebody already in a call or meeting is not rung; you are told they are busy. Missed and declined calls have their own marks in your calls list.',
      },
      {
        area: 'meetings',
        title: 'Meeting minutes',
        body: 'The note-taker starts once two people are in a meeting together, and the minutes are written reliably again.',
        link: '/meeting-notes',
      },
      {
        area: 'code',
        title: 'Tasks follow pull requests',
        body: 'A new pull request for a finished task moves it back to In progress, and merging it moves it to Done again.',
      },
      {
        area: 'chat',
        title: 'Replies and files',
        body: 'Replies in a thread show two ticks once read, and files on a task’s comments download properly.',
      },
    ],
  },
  {
    id: '2026-11-10',
    date: '2026-11-10',
    title: 'Projects, GitHub and Bitbucket',
    changes: [
      {
        area: 'code',
        title: 'Projects with their code',
        body: 'A project can be linked to GitHub or Bitbucket repositories. Each task gets a key such as TEA-2, and a branch, commit or pull request that mentions it shows on the task and moves it along the board.',
        link: '/projects',
      },
      {
        area: 'calendar',
        title: 'Google Calendar',
        body: 'Connect your Google Calendar from the Calendar page to see both side by side.',
        link: '/calendar',
      },
      {
        area: 'tasks',
        title: 'Mentions and pictures',
        body: 'Mention people in a task’s comments with @, and everybody’s picture shows wherever their name does.',
      },
    ],
  },
];

/** The newest entry: what somebody has to have seen for What's new to stay closed. */
export const LATEST = RELEASES[0].id;

/** The entries somebody has not seen, newest first: all of them for somebody who has seen none. */
export function unseen(seen: string | null | undefined): Release[] {
  if (!seen) return RELEASES.slice(0, 1);
  return RELEASES.filter((r) => r.id > seen);
}
