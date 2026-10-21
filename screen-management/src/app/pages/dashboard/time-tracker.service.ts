import { inject, Injectable, signal } from '@angular/core';
import { HttpClient } from '@angular/common/http';
import { firstValueFrom } from 'rxjs';
import { PeopleResponse } from '../../core/api.types';

export interface Employee { id: number; name: string; team: string; teamId: number | null; online: boolean; }
export interface Team { name: string; members: Employee[]; }

/** What the Employees / Teams panel has picked */
export type Selection =
  | { kind: 'team'; team: Team }
  | { kind: 'employee'; employee: Employee };
export interface Segment { start: number; length: number; idle: boolean; }   // minutes from midnight (IST)

export interface DayRecord {
  employee: Employee;
  date: Date;
  absent: boolean;
  late: boolean;         // started after their shift start + grace (decided by the server)
  lateAfter: string;     // "HH:MM" (IST), the last on-time minute of their shift
  inTime: number;        // minutes from midnight (IST)
  finish: number;
  online: number;        // all values below are minutes
  working: number;
  productive: number;
  nonProductive: number;
  neutral: number;
  idle: number;
  approvedAway: number;
  apps: { name: string; minutes: number }[];
  segments: Segment[];
}

const DAY_TZ = 'Asia/Kolkata';    // days are counted in IST; other zones shift what is displayed
const REFRESH_TODAY_MS = 60_000;

/** One day as the server sends it (seconds) — GET /api/timetracker/days */
interface ApiDay {
  userId: number; date: string; inTime: number; finish: number; late: boolean; lateAfter: string;
  working: number; productive: number; nonProductive: number; neutral: number; idle: number;
  apps: { name: string; seconds: number }[];
  segments: { start: number; length: number; idle: boolean }[];
}

const iso = (d: Date) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
/** Today's date in the zone days are counted in, so a browser elsewhere still sees the current day */
const todayIso = () => new Intl.DateTimeFormat('en-CA', { timeZone: DAY_TZ, year: 'numeric', month: '2-digit', day: '2-digit' }).format(new Date());

export function hash(text: string) {
  return [...text].reduce((a, c) => (Math.imul(a, 31) + c.charCodeAt(0)) | 0, 7);
}

export function startOfDay(d: Date) {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate());
}

export function addDays(d: Date, days: number) {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate() + days);
}

/** Minutes → "hh:mm" */
export function hm(minutes: number) {
  const m = Math.round(minutes);
  return `${String(Math.floor(m / 60)).padStart(2, '0')}:${String(m % 60).padStart(2, '0')}`;
}

/**
 * People and their tracked time come from the API: one record per person per day, built by
 * the server from the desktop agent's activity. Records are cached; pages ask for the days
 * they show with load(), then read them with day() / present().
 */
@Injectable({ providedIn: 'root' })
export class TimeTrackerService {
  private readonly http = inject(HttpClient);

  /** Everyone the signed-in user may see. Filled by loadPeople() before any page opens. */
  employees: Employee[] = [];
  teams: Team[] = [];
  withoutTeam = 0;
  /** The signed-in user's own entry: the default selection on every page */
  me!: Employee;

  private loadedFor: number | null = null;

  async loadPeople(userId: number, force = false) {
    if (this.loadedFor === userId && !force) return;
    const res = await firstValueFrom(this.http.get<PeopleResponse>('/api/people'));
    const teamName = new Map(res.teams.map(t => [t.id, t.name]));
    this.employees = res.employees.map(p => ({ id: p.id, name: p.name, online: p.online, teamId: p.teamId, team: teamName.get(p.teamId ?? -1) ?? '' }));
    const byId = new Map(this.employees.map(e => [e.id, e]));
    this.teams = res.teams.map(t => ({ name: t.name, members: t.members.map(m => byId.get(m.id)!) }));
    this.withoutTeam = res.withoutTeam;
    this.me = byId.get(userId) ?? this.employees[0];
    this.loadedFor = userId;
  }

  // ---------- Remembered between pages ----------
  // Every page with the Employees / Teams panel starts from the last pick, so changing
  // tab or page keeps the same person. Kept by id / name so it survives a reload of people.
  private picked: { kind: 'employee'; id: number } | { kind: 'team'; name: string } | null = null;
  /** Day and time zone on the Monitoring tabs */
  monitoringDay: { date: Date; zone: string } | null = null;

  /** The last person or team picked, or the signed-in user */
  selection(): Selection {
    const p = this.picked;
    if (p?.kind === 'team') {
      const team = this.teams.find(t => t.name === p.name);
      if (team) return { kind: 'team', team };
    } else if (p?.kind === 'employee') {
      const employee = this.employees.find(e => e.id === p.id);
      if (employee) return { kind: 'employee', employee };
    }
    return { kind: 'employee', employee: this.me };
  }

  rememberSelection(s: Selection) {
    this.picked = s.kind === 'team' ? { kind: 'team', name: s.team.name } : { kind: 'employee', id: s.employee.id };
  }

  clear() {
    this.picked = null;
    this.monitoringDay = null;
    this.employees = [];
    this.teams = [];
    this.loadedFor = null;
    this.records.clear();
    this.fetched.clear();
  }

  // ---------- Tracked time ----------
  private readonly records = new Map<string, DayRecord>();     // "<user>:<YYYY-MM-DD>", days with activity
  private readonly fetched = new Map<string, number>();        // same keys: when that day was last asked for
  /** Bumped when new records arrive, so pages recompute */
  readonly version = signal(0);
  readonly loading = signal(false);
  readonly error = signal('');

  /** Makes sure the given people's days are loaded (today's again after a minute). */
  async load(members: Employee[], dates: Date[]) {
    const now = Date.now(), today = todayIso();
    const users = new Set<number>(), wanted: Date[] = [];
    for (const d of dates) {
      const day = iso(d);
      if (day > today) continue;
      for (const e of members) {
        const at = this.fetched.get(`${e.id}:${day}`);
        if (at === undefined || (day === today && now - at > REFRESH_TODAY_MS)) {
          users.add(e.id);
          if (!wanted.includes(d)) wanted.push(d);
        }
      }
    }
    if (!users.size || !wanted.length) return;
    const from = wanted.reduce((a, d) => (d < a ? d : a)), to = wanted.reduce((a, d) => (d > a ? d : a));
    this.loading.set(true);
    try {
      const list = await firstValueFrom(this.http.get<ApiDay[]>('/api/timetracker/days', {
        params: { users: [...users].join(','), from: iso(from), to: iso(to), tz: DAY_TZ },
      }));
      const byId = new Map(this.employees.map(e => [e.id, e]));
      // Everything asked for counts as fetched, including days with no activity
      for (let d = new Date(from); d <= to; d.setDate(d.getDate() + 1)) {
        for (const id of users) {
          this.fetched.set(`${id}:${iso(d)}`, now);
          this.records.delete(`${id}:${iso(d)}`);
        }
      }
      for (const a of list) {
        const e = byId.get(a.userId);
        if (e) this.records.set(`${a.userId}:${a.date}`, toRecord(e, a));
      }
      this.error.set('');
    } catch {
      this.error.set('Could not load tracked time. Check that the server is running.');
    } finally {
      this.loading.set(false);
      this.version.update(v => v + 1);
    }
  }

  /** The person's day, or an absent day when nothing was tracked */
  day(employee: Employee, date: Date): DayRecord {
    this.version(); // recompute callers when records arrive
    return this.records.get(`${employee.id}:${iso(date)}`) ?? absent(employee, date);
  }

  /** Days the given people were present, across the given dates */
  present(members: Employee[], dates: Date[]): DayRecord[] {
    return members.flatMap(e => dates.map(d => this.day(e, d))).filter(r => !r.absent);
  }
}

function absent(employee: Employee, date: Date): DayRecord {
  return {
    employee, date: startOfDay(date), absent: true, late: false, lateAfter: '', inTime: 0, finish: 0,
    online: 0, working: 0, productive: 0, nonProductive: 0, neutral: 0, idle: 0, approvedAway: 0, apps: [], segments: [],
  };
}

const min = (seconds: number) => seconds / 60;

function toRecord(employee: Employee, a: ApiDay): DayRecord {
  const [y, m, d] = a.date.split('-').map(Number);
  const inTime = min(a.inTime);
  return {
    employee, date: new Date(y, m - 1, d), absent: false, late: a.late, lateAfter: a.lateAfter,
    inTime, finish: min(a.finish), online: min(a.finish - a.inTime),
    working: min(a.working), productive: min(a.productive), nonProductive: min(a.nonProductive),
    neutral: min(a.neutral), idle: min(a.idle), approvedAway: 0,   // time-off approvals are not built yet
    apps: a.apps.map(x => ({ name: x.name, minutes: min(x.seconds) })),
    segments: a.segments.map(x => ({ start: min(x.start), length: min(x.length), idle: x.idle })),
  };
}
