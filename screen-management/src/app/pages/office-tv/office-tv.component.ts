import { Component, computed, DestroyRef, ElementRef, HostListener, inject, signal } from '@angular/core';
import { MatIconModule } from '@angular/material/icon';
import { MatTooltipModule } from '@angular/material/tooltip';
import { firstValueFrom } from 'rxjs';
import { errorMessage } from '../../core/auth.service';
import { avatarColor, clock, Desktop, hm, initials, nowIn, WorkDay, WorkplaceService } from '../../core/workplace.service';
import { TimeTrackerService } from '../dashboard/time-tracker.service';

const TZ = 'Asia/Kolkata';          // days are counted in IST, as on the Time Tracker pages
const REFRESH_MS = 60_000;
const ACTIVE_WITHIN_SEC = 5 * 60;   // activity this recent counts as working now

type Status = 'working' | 'idle' | 'left' | 'absent';

const STATUS_LABEL: Record<Status, string> = {
  working: 'Working', idle: 'Idle', left: 'Signed off', absent: 'Not started',
};
const STATUS_ORDER: Record<Status, number> = { working: 0, idle: 1, left: 2, absent: 3 };

/** Office TV: a live, full-screen board of today's work for a screen on the office wall */
@Component({
  selector: 'app-office-tv',
  standalone: true,
  imports: [MatIconModule, MatTooltipModule],
  templateUrl: './office-tv.component.html',
  styleUrl: './office-tv.component.scss',
})
export class OfficeTvComponent {
  private readonly api = inject(WorkplaceService);
  private readonly people = inject(TimeTrackerService);
  private readonly host = inject(ElementRef<HTMLElement>);

  readonly tz = TZ;
  now = signal(new Date());
  days = signal<WorkDay[]>([]);
  devices = signal<Desktop[]>([]);
  updatedAt = signal<Date | null>(null);
  loading = signal(true);
  error = signal('');
  fullscreen = signal(false);
  team = signal<string>('');   // '' = everyone

  readonly teams = computed(() => this.people.teams.map(t => t.name));

  readonly clock = computed(() => new Intl.DateTimeFormat('en-GB', { timeZone: TZ, hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).format(this.now()));
  readonly seconds = computed(() => new Intl.DateTimeFormat('en-GB', { timeZone: TZ, second: '2-digit' }).format(this.now()).padStart(2, '0'));
  readonly dateLabel = computed(() => new Intl.DateTimeFormat('en-GB', { timeZone: TZ, weekday: 'long', day: 'numeric', month: 'long', year: 'numeric' }).format(this.now()));

  /** Everyone on the board with today's numbers */
  readonly board = computed(() => {
    const today = nowIn(TZ, this.now());
    const byUser = new Map(this.days().filter(d => d.date === today.date).map(d => [d.userId, d]));
    const online = new Set(this.devices().filter(d => d.online).map(d => d.user.id));
    const team = this.team();
    return this.people.employees
      .filter(e => !team || e.team === team)
      .map(e => {
        const day = byUser.get(e.id);
        const recent = !!day && today.seconds - day.finish <= ACTIVE_WITHIN_SEC;
        const status: Status = online.has(e.id) ? (recent ? 'working' : 'idle') : day ? (recent ? 'working' : 'left') : 'absent';
        const working = day?.working ?? 0;
        return {
          id: e.id, name: e.name, team: e.team, status, statusLabel: STATUS_LABEL[status],
          initials: initials(e.name), color: avatarColor(e.id),
          working, workingLabel: hm(working),
          inTime: day ? clock(day.inTime) : '',
          lastSeen: day ? clock(day.finish) : '',
          topApp: day?.apps[0]?.name ?? '',
          productive: day?.productive ?? 0,
          share: working ? {
            productive: (day!.productive / working) * 100,
            neutral: (day!.neutral / working) * 100,
            unproductive: (day!.nonProductive / working) * 100,
          } : null,
          score: working ? Math.round((day!.productive / working) * 100) : 0,
        };
      })
      .sort((a, b) => STATUS_ORDER[a.status] - STATUS_ORDER[b.status] || b.working - a.working || a.name.localeCompare(b.name));
  });

  readonly kpis = computed(() => {
    const b = this.board();
    const worked = b.filter(p => p.working > 0);
    const total = worked.reduce((s, p) => s + p.working, 0);
    const productive = worked.reduce((s, p) => s + p.productive, 0);
    return {
      people: b.length,
      working: b.filter(p => p.status === 'working').length,
      idle: b.filter(p => p.status === 'idle').length,
      present: worked.length,
      absent: b.filter(p => p.status === 'absent').length,
      hours: hm(total),
      average: worked.length ? hm(total / worked.length) : '0m',
      productivity: total ? Math.round((productive / total) * 100) : 0,
    };
  });

  /** Most productive time today */
  readonly leaders = computed(() => this.board().filter(p => p.productive > 0)
    .sort((a, b) => b.productive - a.productive).slice(0, 5)
    .map(p => ({ ...p, productiveLabel: hm(p.productive) })));

  /** Applications used most today, from each person's top apps */
  readonly apps = computed(() => {
    const ids = new Set(this.board().map(p => p.id));
    const today = nowIn(TZ, this.now()).date;
    const sums = new Map<string, number>();
    for (const d of this.days()) {
      if (d.date !== today || !ids.has(d.userId)) continue;
      for (const a of d.apps) sums.set(a.name, (sums.get(a.name) ?? 0) + a.seconds);
    }
    const list = [...sums].sort((a, b) => b[1] - a[1]).slice(0, 6);
    const top = list[0]?.[1] ?? 1;
    return list.map(([name, seconds]) => ({ name, label: hm(seconds), width: (seconds / top) * 100 }));
  });

  /** Present and productivity per team */
  readonly teamRows = computed(() => {
    const groups = new Map<string, { total: number; present: number; working: number; productive: number }>();
    for (const p of this.board()) {
      const g = groups.get(p.team || 'No team') ?? { total: 0, present: 0, working: 0, productive: 0 };
      g.total++;
      if (p.working) g.present++;
      g.working += p.working;
      g.productive += p.productive;
      groups.set(p.team || 'No team', g);
    }
    return [...groups].map(([name, g]) => ({
      name, ...g, score: g.working ? Math.round((g.productive / g.working) * 100) : 0,
    })).sort((a, b) => b.present - a.present || a.name.localeCompare(b.name));
  });

  constructor() {
    this.refresh();
    const tick = setInterval(() => this.now.set(new Date()), 1000);
    const poll = setInterval(() => this.refresh(), REFRESH_MS);
    inject(DestroyRef).onDestroy(() => { clearInterval(tick); clearInterval(poll); });
  }

  async refresh() {
    const today = nowIn(TZ).date;
    try {
      const [days, devices] = await Promise.all([
        firstValueFrom(this.api.days({ from: today, to: today, tz: TZ }, true)),
        firstValueFrom(this.api.desktops('active', true)),
      ]);
      this.days.set(days);
      this.devices.set(devices.items);
      this.updatedAt.set(new Date());
      this.error.set('');
    } catch (err) {
      this.error.set(errorMessage(err, 'Could not refresh the board.'));
    } finally {
      this.loading.set(false);
    }
  }

  updatedLabel() {
    const at = this.updatedAt();
    return at ? new Intl.DateTimeFormat('en-GB', { timeZone: TZ, hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).format(at) : '–';
  }

  async toggleFullscreen() {
    const el = this.host.nativeElement.querySelector('.tv') as HTMLElement | null;
    if (document.fullscreenElement) await document.exitFullscreen();
    else await el?.requestFullscreen?.().catch(() => undefined);
  }

  @HostListener('document:fullscreenchange')
  onFullscreenChange() { this.fullscreen.set(!!document.fullscreenElement); }
}
