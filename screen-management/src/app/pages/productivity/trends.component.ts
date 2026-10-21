import { Component, computed, effect, inject, signal } from '@angular/core';
import { DecimalPipe, formatDate } from '@angular/common';
import { RouterLink } from '@angular/router';
import { MatIconModule } from '@angular/material/icon';
import { MatTooltipModule } from '@angular/material/tooltip';
import { Productivity } from '../../core/activity.service';
import { AuthService } from '../../core/auth.service';
import { BreakdownRow, ProductivityService } from '../../core/productivity.service';
import { Employee, hm, TimeTrackerService } from '../dashboard/time-tracker.service';
import { PeoplePanelComponent, Selection } from '../people-panel/people-panel.component';
import { loadOnChange } from '../shared/load';
import { isoDate, PeriodFilter } from '../shared/period';
import { PeriodToolbarComponent } from '../shared/period-toolbar.component';

const DONUT_R = 40;
const DONUT_C = 2 * Math.PI * DONUT_R;

export const LEVELS: { key: Productivity; label: string; icon: string; color: string }[] = [
  { key: 'productive', label: 'Productive', icon: 'check_circle', color: '#16a34a' },
  { key: 'unproductive', label: 'Unproductive', icon: 'cancel', color: '#dc2626' },
  { key: 'neutral', label: 'Neutral', icon: 'remove_circle', color: '#94a3b8' },
];

const CATEGORY_COLORS = ['#4f46e5', '#0ea5e9', '#f97316', '#14b8a6', '#a855f7', '#ec4899', '#22c55e', '#eab308', '#64748b', '#ef4444'];

export const PRODUCTIVITY_TABS = [
  { key: 'trends', label: 'Trends', icon: 'trending_up', route: '/trends' },
  { key: 'configure-apps', label: 'Configure Apps', icon: 'tune', route: '/configure-apps' },
];

/** Productivity > Trends, from GET /api/productivity/trends */
@Component({
  selector: 'app-trends',
  standalone: true,
  imports: [DecimalPipe, RouterLink, MatIconModule, MatTooltipModule, PeoplePanelComponent, PeriodToolbarComponent],
  templateUrl: './trends.component.html',
  // Layout, tabs and cards come from the Time Tracker page
  styleUrls: ['../dashboard/dashboard.component.scss', './trends.component.scss'],
})
export class TrendsComponent {
  private readonly people = inject(TimeTrackerService);
  private readonly api = inject(ProductivityService);
  private readonly auth = inject(AuthService);

  readonly tabs = PRODUCTIVITY_TABS.filter(t => this.auth.can(t.route));
  readonly levels = LEVELS;
  readonly donutR = DONUT_R;

  selection = signal<Selection>(this.people.selection());
  // Keep the pick when moving to another tab or page
  private readonly keepSelection = effect(() => this.people.rememberSelection(this.selection()));

  readonly period = new PeriodFilter();
  private attempt = signal(0);

  members = computed<Employee[]>(() => {
    const s = this.selection();
    return s.kind === 'employee' ? [s.employee] : s.team.members;
  });

  title = computed(() => {
    const s = this.selection();
    return s.kind === 'employee' ? s.employee.name : `${s.team.name} (${s.team.members.length})`;
  });

  private readonly request = computed(() => ({
    ...this.period.apiRange(),
    users: this.members().map(m => m.id),
    attempt: this.attempt(),
  }));

  readonly trends = loadOnChange(this.request, r => this.api.trends(r));

  retry() { this.attempt.update(n => n + 1); }

  // ---------- Tiles ----------
  tiles = computed(() => {
    const t = this.trends().data?.totals;
    const v = (s?: number) => this.hm(s ?? 0);
    return [
      { label: 'Working Time', value: v(t?.working), icon: 'schedule', tone: 'blue' },
      { label: 'Productive Time', value: v(t?.productive), icon: 'check_circle', tone: 'green' },
      { label: 'Unproductive Time', value: v(t?.unproductive), icon: 'cancel', tone: 'red' },
      { label: 'Neutral Time', value: v(t?.neutral), icon: 'remove_circle', tone: 'grey' },
      { label: 'Away Time', value: v(t?.away), icon: 'free_breakfast', tone: 'amber' },
    ];
  });

  // ---------- Productivity Bar ----------
  /** Stacked bars: how much of each slot (5 minutes on one day) or day (8h) was spent at each level */
  bars = computed(() => {
    const b = this.trends().data?.bars;
    if (!b?.items.length || !b.items.some(i => i.productive + i.neutral + i.unproductive)) return null;
    const slot = b.unit === 'slot';
    const every = slot ? 3600 : 0;
    const dense = b.items.length > 40;
    return {
      unit: b.unit,
      items: b.items.map((it, n) => {
        const pct = (v: number) => Math.min((v / it.capacity) * 100, 100);
        const when = slot ? `${this.clock(it.start ?? 0)} – ${this.clock((it.start ?? 0) + b.slot)}` : formatDate(it.date + 'T00:00:00', 'EEE, d MMM', 'en-US');
        const used = it.productive + it.neutral + it.unproductive;
        const tick = slot ? (it.start ?? 0) % every === 0 : !dense || n % 7 === 0;
        return {
          key: slot ? String(it.start) : it.date!,
          productive: pct(it.productive), unproductive: pct(it.unproductive), neutral: pct(it.neutral),
          label: tick ? (slot ? this.clock(it.start ?? 0) : formatDate(it.date + 'T00:00:00', dense ? 'd MMM' : 'EEE d', 'en-US')) : '',
          tip: `${when} · ${this.hm(used)} active (${Math.round(Math.min(used / it.capacity, 1) * 100)}%)`
            + ` · productive ${this.hm(it.productive)}, unproductive ${this.hm(it.unproductive)}, neutral ${this.hm(it.neutral)}`,
        };
      }),
    };
  });

  // ---------- Breakdown column titles (shared by both breakdown tables) ----------
  heads = computed(() => {
    const rows = this.trends().data?.breakdown;
    if (!rows?.length) return [];
    const isToday = rows[0].to === isoDate(this.period.today);
    const day = (iso: string, pattern: string) => formatDate(iso + 'T00:00:00', pattern, 'en-US');
    return [
      isToday ? 'Today' : day(rows[0].to, 'EEE, d MMM'),
      isToday ? 'Yesterday' : day(rows[1].to, 'EEE, d MMM'),
      isToday ? 'This Week' : `Week to ${day(rows[2].to, 'd MMM')}`,
    ];
  });

  // ---------- Categories ----------
  private categoryColor = computed(() => {
    const names = this.trends().data?.categories.breakdown.map(c => c.name) ?? [];
    return new Map(names.map((n, i) => [n, CATEGORY_COLORS[i % CATEGORY_COLORS.length]]));
  });

  categoryDonut = computed(() => {
    const list = this.trends().data?.categories.period ?? [];
    const total = list.reduce((a, c) => a + c.seconds, 0);
    if (!total) return [];
    let acc = 0;
    return list.map(c => {
      const len = (c.seconds / total) * DONUT_C;
      const slice = { ...c, color: this.categoryColor().get(c.name)!, dash: `${len} ${DONUT_C - len}`, offset: -acc };
      acc += len;
      return slice;
    });
  });

  categoryRows = computed(() => (this.trends().data?.categories.breakdown ?? []).map(r => ({
    ...r, color: this.categoryColor().get(r.name)!,
  })));

  // ---------- Daily timelines (one person) ----------
  dayRows = computed(() => {
    const days = this.trends().data?.days;
    if (!days) return null;
    const all = days.flatMap(d => d.segments);
    const first = Math.min(9 * 3600, ...all.map(s => Math.floor(s.start / 3600) * 3600));
    const last = Math.max(18 * 3600, ...all.map(s => Math.ceil(s.end / 3600) * 3600));
    const span = last - first;
    const hours = [];
    for (let h = first; h < last; h += 3600) hours.push({ left: ((h - first) / span) * 100, label: this.hour(h) });
    const selected = this.period.range() === 'Day' ? isoDate(this.period.date()) : '';
    return {
      hours,
      rows: days.map(d => ({
        date: d.date,
        label: formatDate(d.date + 'T00:00:00', 'EEE, d MMM', 'en-US'),
        selected: d.date === selected,
        worked: this.hm(d.segments.reduce((a, s) => a + s.end - s.start, 0)),
        segments: d.segments.map(s => ({
          ...s,
          left: ((s.start - first) / span) * 100,
          width: Math.max(((s.end - s.start) / span) * 100, 0.12),
          tip: `${this.clock(s.start)} – ${this.clock(s.end)} · ${s.productivity}`,
        })),
      })),
    };
  });

  // ---------- Donut ----------
  donut = computed(() => {
    const t = this.trends().data?.totals;
    if (!t?.working) return [];
    let acc = 0;
    return LEVELS.map(l => {
      const value = t[l.key];
      const len = (value / t.working) * DONUT_C;
      const slice = { ...l, value, pct: (value / t.working) * 100, dash: `${len} ${DONUT_C - len}`, offset: -acc };
      acc += len;
      return slice;
    });
  });

  // ---------- Breakdown ----------
  breakdown = computed(() => {
    const rows = this.trends().data?.breakdown;
    if (!rows?.length) return null;
    const heads = this.heads();
    const cells = (key: 'working' | Productivity | 'away') => rows.map((r: BreakdownRow) => {
      const share = key === 'working' ? (r.expected ? r.working / r.expected : 0) : r.working + r.away ? r[key] / (r.working + r.away) : 0;
      return { value: this.hm(r[key]), pct: share * 100, below: key === 'working' && r.working < r.expected, expected: this.hm(r.expected) };
    });
    return {
      heads,
      rows: [
        { label: 'Working', color: '#4f46e5', key: 'working', cells: cells('working') },
        ...LEVELS.map(l => ({ label: l.label, color: l.color, key: l.key, cells: cells(l.key) })),
        { label: 'Away', color: '#f59e0b', key: 'away', cells: cells('away') },
      ],
    };
  });

  // ---------- Helpers ----------
  hm(seconds: number) { return hm(seconds / 60); }

  private hour(seconds: number) {
    const h = Math.floor(seconds / 3600) % 24;
    return `${h % 12 || 12} ${h < 12 ? 'AM' : 'PM'}`;
  }

  private clock(seconds: number) {
    const h = Math.floor(seconds / 3600) % 24, m = Math.floor((seconds % 3600) / 60);
    return `${h % 12 || 12}:${String(m).padStart(2, '0')} ${h < 12 ? 'AM' : 'PM'}`;
  }
}
