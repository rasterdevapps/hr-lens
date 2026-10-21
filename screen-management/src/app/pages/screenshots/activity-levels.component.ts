import { pipeOffset } from '../shared/period';
import { Component, computed, effect, inject, input, signal, untracked } from '@angular/core';
import { formatDate } from '@angular/common';
import { MatIconModule } from '@angular/material/icon';
import { MatMenuModule } from '@angular/material/menu';
import { MatTooltipModule } from '@angular/material/tooltip';
import { ActivityService, Productivity } from '../../core/activity.service';
import { MonitoringService } from '../../core/monitoring.service';
import { Employee } from '../dashboard/time-tracker.service';
import { loadOnChange } from '../shared/load';
import { isoDate } from '../shared/period';
import type { Zone } from './screenshots.component';

type Filter = Productivity | 'all';

const BUCKET = 600;      // seconds per chart point (the server's bucket)
const PAGE_SIZE = 30;

const pad = (n: number) => String(Math.floor(n)).padStart(2, '0');
const hms = (sec: number) => `${pad(sec / 3600)}:${pad((sec % 3600) / 60)}:${pad(sec % 60)}`;

/** Monitoring > Activity Levels: key presses and clicks through the day, and every window used */
@Component({
  selector: 'app-activity-levels',
  standalone: true,
  imports: [MatIconModule, MatMenuModule, MatTooltipModule],
  templateUrl: './activity-levels.component.html',
  styleUrl: './activity-levels.component.scss',
})
export class ActivityLevelsComponent {
  private readonly monitoring = inject(MonitoringService);
  private readonly activity = inject(ActivityService);

  members = input.required<Employee[]>();
  date = input.required<Date>();
  zone = input.required<Zone>();

  readonly filters: { value: Filter; label: string }[] = [
    { value: 'all', label: 'All' },
    { value: 'productive', label: 'Productive' },
    { value: 'neutral', label: 'Neutral' },
    { value: 'unproductive', label: 'Unproductive' },
  ];
  readonly icons: Record<Productivity, string> = { productive: 'check_circle', neutral: 'remove_circle', unproductive: 'cancel' };

  filter = signal<Filter>('all');
  newestFirst = signal(true);
  page = signal(1);
  private picked = signal<number | null>(null);
  private attempt = signal(0);

  /** Activity is per person: with a team selected, one member is shown at a time */
  employee = computed(() => this.members().find(e => e.id === this.picked()) ?? this.members()[0]);

  private readonly day = computed(() => ({
    users: [this.employee().id], from: isoDate(this.date()), to: isoDate(this.date()), tz: this.zone().tz, attempt: this.attempt(),
  }));

  constructor() {
    // Back to the first page when the person, day or filter changes
    effect(() => {
      this.day();
      this.filter();
      untracked(() => this.page.set(1));
    });
  }

  retry() { this.attempt.update(n => n + 1); }

  // ---------- Chart ----------
  readonly input = loadOnChange(this.day, d => this.monitoring.input(d));

  /** Key presses and clicks per 10 minutes, from the first to the last active bucket */
  chart = computed(() => {
    const list = this.input().data ?? [];
    if (!list.some(b => b.keys || b.clicks)) return null;
    const byStart = new Map(list.map(b => [b.start, b]));
    const first = list[0].start, last = list[list.length - 1].start;
    const points: { time: string; keys: number; clicks: number }[] = [];
    for (let t = first; t <= last; t += BUCKET) {
      const b = byStart.get(t);
      points.push({ time: `${pad(t / 3600)}:${pad((t % 3600) / 60)}`, keys: b?.keys ?? 0, clicks: b?.clicks ?? 0 });
    }
    if (points.length === 1) points.push({ ...points[0], time: `${pad((first + BUCKET) / 3600)}:${pad(((first + BUCKET) % 3600) / 60)}` });

    const peak = Math.max(10, ...points.map(p => Math.max(p.keys, p.clicks)));
    const step = peak <= 50 ? 10 : peak <= 700 ? Math.ceil(peak / 700 * 7) * 10 : Math.ceil(peak / 700) * 100;
    const max = Math.ceil(peak / step) * step;
    const area = (key: 'keys' | 'clicks') => 'M0,100 '
      + points.map((p, i) => `L${((i / (points.length - 1)) * 1000).toFixed(1)},${(100 - (p[key] / max) * 100).toFixed(1)}`).join(' ')
      + ' L1000,100 Z';
    const every = Math.ceil(points.length / 16);

    return {
      points,
      keys: area('keys'),
      clicks: area('clicks'),
      totalKeys: list.reduce((a, b) => a + b.keys, 0),
      totalClicks: list.reduce((a, b) => a + b.clicks, 0),
      ticks: Array.from({ length: max / step + 1 }, (_, i) => max - i * step),
      labels: points.map((p, i) => ({ text: p.time, left: (i / (points.length - 1)) * 100, show: i % every === 0 || i === points.length - 1 }))
        .filter(l => l.show),
    };
  });

  // ---------- Log ----------
  private readonly logRequest = computed(() => ({
    ...this.day(), productivity: this.filter() === 'all' ? '' as const : this.filter() as Productivity,
    search: '', page: this.page(), dir: this.newestFirst() ? 'desc' as const : 'asc' as const,
  }));
  readonly log = loadOnChange(this.logRequest, r => this.activity.events(r, r.page - 1, PAGE_SIZE, r.dir));

  total = computed(() => this.log().data?.total ?? 0);
  pageCount = computed(() => Math.max(1, Math.ceil(this.total() / PAGE_SIZE)));

  /** Up to 10 page numbers around the current page */
  pages = computed(() => {
    const count = this.pageCount();
    const start = Math.max(1, Math.min(this.page() - 4, count - 9));
    return Array.from({ length: Math.min(10, count) }, (_, i) => start + i);
  });

  rows = computed(() => {
    const z = this.zone();
    return (this.log().data?.items ?? []).map(e => ({
      id: e.id,
      productivity: e.productivity,
      label: this.filters.find(f => f.value === e.productivity)!.label,
      icon: this.icons[e.productivity],
      date: `${formatDate(e.startedAt, 'dd-MMM-y HH:mm:ss', 'en-US', pipeOffset(z.tz, new Date(e.startedAt)))} ${z.code}`,
      duration: hms(e.seconds),
      title: e.title || e.appName,
      description: e.domain ? `${e.app} · ${e.domain}` : e.app,
    }));
  });

  range = computed(() => {
    const from = (this.page() - 1) * PAGE_SIZE;
    return this.total() ? `${from + 1} - ${Math.min(from + PAGE_SIZE, this.total())} of ${this.total()} Items` : '0 Items';
  });

  filterLabel = computed(() => this.filters.find(f => f.value === this.filter())!.label);

  pick(e: Employee) { this.picked.set(e.id); }

  go(page: number) {
    this.page.set(Math.min(this.pageCount(), Math.max(1, page)));
  }
}
