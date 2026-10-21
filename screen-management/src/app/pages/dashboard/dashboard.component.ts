import { zoneOffsetMinutes } from '../shared/period';
import { Component, computed, effect, inject, signal } from '@angular/core';
import { DatePipe, formatDate } from '@angular/common';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormControl, FormGroup, ReactiveFormsModule } from '@angular/forms';
import { ActivatedRoute, RouterLink } from '@angular/router';
import { provideNativeDateAdapter } from '@angular/material/core';
import { MatDatepicker, MatDatepickerModule, MatDateRangePicker } from '@angular/material/datepicker';
import { MatIconModule } from '@angular/material/icon';
import { MatMenuModule } from '@angular/material/menu';
import { MatTooltipModule } from '@angular/material/tooltip';
import { AttendanceComponent } from './attendance/attendance.component';
import { HeatMapsComponent } from './heat-maps/heat-maps.component';
import { PeoplePanelComponent, Selection } from '../people-panel/people-panel.component';
import { addDays, DayRecord, Employee, hm, startOfDay, TimeTrackerService } from './time-tracker.service';

type Range = 'Day' | 'Week' | 'Month' | 'Date Range';

interface Slice { label: string; value: number; color: string; pct: number; dash: string; offset: number; }

const EXPECTED_HOURS = 8;
const DONUT_R = 40;
const DONUT_C = 2 * Math.PI * DONUT_R;
const APP_COLORS = ['#4f46e5', '#0ea5e9', '#f97316', '#14b8a6', '#a855f7'];
const DAY_MS = 86400000;

// Times are recorded in IST; other zones shift what is displayed
const ZONES = [
  { code: 'IST', label: 'India (UTC+5:30)', tz: 'Asia/Kolkata' },
  { code: 'GST', label: 'Gulf (UTC+4:00)', tz: 'Asia/Dubai' },
  { code: 'UTC', label: 'UTC', tz: 'UTC' },
  { code: 'EST', label: 'US Eastern', tz: 'America/New_York' },
  { code: 'PST', label: 'US Pacific', tz: 'America/Los_Angeles' },
];

function donut(items: { label: string; value: number; color: string }[]): Slice[] {
  const total = items.reduce((a, i) => a + i.value, 0);
  let acc = 0;
  return items.map(i => {
    const len = total ? (i.value / total) * DONUT_C : 0;
    const slice = {
      ...i,
      pct: total ? Math.round((i.value / total) * 100) : 0,
      dash: `${len} ${DONUT_C - len}`,
      offset: -acc,
    };
    acc += len;
    return slice;
  });
}

const sum = (list: DayRecord[], key: keyof DayRecord) => list.reduce((a, r) => a + (r[key] as number), 0);
const fmt = (d: Date, pattern: string) => formatDate(d, pattern, 'en-US');

@Component({
  selector: 'app-dashboard',
  standalone: true,
  imports: [
    DatePipe, RouterLink, ReactiveFormsModule, MatDatepickerModule, MatIconModule, MatMenuModule, MatTooltipModule,
    AttendanceComponent, HeatMapsComponent, PeoplePanelComponent,
  ],
  providers: [provideNativeDateAdapter()],
  templateUrl: './dashboard.component.html',
  styleUrl: './dashboard.component.scss',
})
export class DashboardComponent {
  private readonly data = inject(TimeTrackerService);

  readonly ranges: Range[] = ['Day', 'Week', 'Month', 'Date Range'];
  readonly zones = ZONES;
  readonly donutR = DONUT_R;
  readonly today = startOfDay(new Date());
  readonly hm = hm;

  readonly tabs = [
    { key: 'dashboard', label: 'Dashboard', icon: 'dashboard', route: '/dashboard' },
    { key: 'attendance', label: 'Attendance', icon: 'calendar_month', route: '/attendance' },
    { key: 'time-claim', label: 'Time Claim', icon: 'event_available', route: '/time-claim' },
    { key: 'heat-maps', label: 'Heat Maps', icon: 'grid_view', route: '/heat-maps' },
  ];
  readonly tab: string = inject(ActivatedRoute).snapshot.data['tab'] ?? 'dashboard';
  readonly tabLabel = this.tabs.find(t => t.key === this.tab)?.label ?? '';

  // ---------- Filters ----------
  selection = signal<Selection>(this.data.selection());
  // Keep the pick when moving to another tab or page
  private readonly keepSelection = effect(() => this.data.rememberSelection(this.selection()));


  range = signal<Range>(this.tab === 'attendance' ? 'Month' : 'Day');
  date = signal(this.today);
  from = signal(addDays(this.today, -6));   // custom Date Range
  to = signal(this.today);
  zone = signal(ZONES[0]);

  readonly rangeForm = new FormGroup({
    start: new FormControl<Date | null>(this.from()),
    end: new FormControl<Date | null>(this.to()),
  });

  constructor() {
    this.rangeForm.valueChanges.pipe(takeUntilDestroyed()).subscribe(({ start, end }) => {
      if (start && end) {
        this.from.set(startOfDay(start));
        this.to.set(startOfDay(end));
      }
    });
  }

  // ---------- Selection ----------
  members = computed<Employee[]>(() => {
    const s = this.selection();
    return s.kind === 'employee' ? [s.employee] : s.team.members;
  });

  title = computed(() => {
    const s = this.selection();
    return s.kind === 'employee' ? s.employee.name : `${s.team.name} (${s.team.members.length})`;
  });

  // ---------- Period ----------
  /** First and last day of the selected period (the end may be in the future) */
  private period = computed(() => {
    const d = this.date();
    switch (this.range()) {
      case 'Week': {
        const monday = addDays(d, -((d.getDay() + 6) % 7));
        return { from: monday, to: addDays(monday, 6) };
      }
      case 'Month': return { from: new Date(d.getFullYear(), d.getMonth(), 1), to: new Date(d.getFullYear(), d.getMonth() + 1, 0) };
      case 'Date Range': return { from: this.from(), to: this.to() };
      default: return { from: d, to: d };
    }
  });

  /** Every day of the period, up to today */
  dates = computed(() => {
    const { from, to } = this.period();
    const last = to > this.today ? this.today : to;
    const list: Date[] = [];
    for (let d = from; d <= last; d = addDays(d, 1)) list.push(d);
    return list;
  });

  atEnd = computed(() => this.period().to >= this.today);

  periodLabel = computed(() => {
    const { from, to } = this.period();
    switch (this.range()) {
      case 'Day': return fmt(from, 'd MMM y');
      case 'Month': return fmt(from, 'MMMM y');
      default: return `${fmt(from, from.getFullYear() === to.getFullYear() ? 'd MMM' : 'd MMM y')} – ${fmt(to, 'd MMM y')}`;
    }
  });

  setRange(range: Range, rangePicker: MatDateRangePicker<Date>) {
    this.range.set(range);
    if (range === 'Date Range') rangePicker.open();
  }

  openPicker(picker: MatDatepicker<Date>, rangePicker: MatDateRangePicker<Date>) {
    (this.range() === 'Date Range' ? rangePicker : picker).open();
  }

  monthPicked(month: Date, picker: MatDatepicker<Date>) {
    if (this.range() !== 'Month') return;
    this.date.set(month > this.today ? this.today : month);
    picker.close();
  }

  /** Moves one day, week, month, or the length of the custom range */
  shift(step: number) {
    if (this.range() === 'Date Range') {
      const length = Math.round((this.to().getTime() - this.from().getTime()) / DAY_MS) + 1;
      let to = addDays(this.to(), step * length);
      if (to > this.today) to = this.today;
      this.rangeForm.setValue({ start: addDays(to, -(length - 1)), end: to });
      return;
    }
    const d = this.date();
    const next = this.range() === 'Month' ? new Date(d.getFullYear(), d.getMonth() + step, 1)
      : addDays(d, step * (this.range() === 'Week' ? 7 : 1));
    this.date.set(next > this.today ? this.today : next);
  }

  // ---------- Time zone ----------
  /** Minutes to add to IST clock times for the chosen zone, as of the selected day (follows daylight saving) */
  tzShift = computed(() => zoneOffsetMinutes(this.zone().tz, this.date()) - zoneOffsetMinutes(ZONES[0].tz, this.date()));

  clock(minutes: number) {
    return hm((((minutes + this.tzShift()) % 1440) + 1440) % 1440);
  }

  // ---------- Dashboard data ----------
  /** Fetch the days on screen: the period, plus the 8 days the Day charts show */
  private readonly loadDays = effect(() => {
    const dates = [...this.dates()];
    if (this.range() === 'Day') for (let i = 1; i < 8; i++) dates.push(addDays(this.date(), -i));
    this.data.load(this.members(), dates);
  }, { allowSignalWrites: true });
  readonly loadError = this.data.error;

  private present = computed(() => this.data.present(this.members(), this.dates()));

  stats = computed(() => {
    const list = this.present(), n = list.length;
    const avgClock = (key: 'inTime' | 'finish') => (n ? this.clock(sum(list, key) / n) : '--:--');
    const avg = n > 1 ? ' (Avg)' : '';
    return [
      { label: 'Start Time' + avg, value: avgClock('inTime'), tone: 'sky',
        hint: 'When they first signed in to or unlocked their computer that day (their first activity if the agent did not report it)' },
      { label: 'Working Time', value: hm(sum(list, 'working')), tone: 'blue' },
      { label: 'Last Seen' + avg, value: avgClock('finish'), tone: 'orange', hint: 'End of their last activity that day' },
      { label: 'Productive Time', value: hm(sum(list, 'productive')), tone: 'green' },
      { label: 'Non-Productive Time', value: hm(sum(list, 'nonProductive')), tone: 'red' },
      { label: 'Neutral Time', value: hm(sum(list, 'neutral')), tone: 'lavender' },
      { label: 'Away Time', value: hm(sum(list, 'idle')), tone: 'grey' },
    ];
  });

  topApps = computed(() => {
    const totals = new Map<string, number>();
    for (const r of this.present()) for (const a of r.apps) totals.set(a.name, (totals.get(a.name) ?? 0) + a.minutes);
    const top = [...totals].filter(([, m]) => m > 0).sort((a, b) => b[1] - a[1]).slice(0, 5);
    return donut(top.map(([label, value], i) => ({ label, value, color: APP_COLORS[i] })));
  });

  activity = computed(() => {
    const list = this.present();
    return donut([
      { label: 'Desk Time', value: sum(list, 'working'), color: '#4f46e5' },
      { label: 'Productive Time', value: sum(list, 'productive'), color: '#22c55e' },
      { label: 'Non-Productive Time', value: sum(list, 'nonProductive'), color: '#f59e0b' },
      { label: 'Neutral Time', value: sum(list, 'neutral'), color: '#94a3b8' },
    ]);
  });

  hasData = computed(() => this.present().length > 0);

  /** One row per person for the tables */
  rows = computed(() => this.members().map(e => {
    const list = this.data.present([e], this.dates()), n = list.length;
    const avg = (key: keyof DayRecord) => (n ? sum(list, key) / n : 0);
    const hours = list.map(r => r.working / 60);
    return {
      name: e.name,
      manDays: n,
      working: hm(avg('working')),
      approvedAway: hm(avg('approvedAway')),
      idle: hm(avg('idle')),
      awayTotal: hm(sum(list, 'idle')),
      over: hours.filter(h => h > 8).length,
      healthy: hours.filter(h => h >= 6 && h <= 8).length,
      under: hours.filter(h => h < 6).length,
    };
  }));

  /** Per-day averages for the bar charts. A single day shows the 8 days leading up to it. */
  private chartDays = computed(() => {
    const dates = this.range() === 'Day'
      ? Array.from({ length: 8 }, (_, i) => addDays(this.date(), i - 7))
      : this.dates();
    const label = dates.length <= 10 ? 'yyyy-MM-dd' : dates.length <= 16 ? 'd MMM' : 'd';
    return dates.map(date => {
      const list = this.data.present(this.members(), [date]), n = list.length || 1;
      return {
        date, label: fmt(date, label), tip: fmt(date, 'EEE, d MMM y'),
        worked: sum(list, 'working') / n / 60,
        away: sum(list, 'idle') / n / 60,
      };
    });
  });

  workChart = computed(() => {
    const days = this.chartDays();
    const max = Math.max(EXPECTED_HOURS + 1, Math.ceil(Math.max(0, ...days.map(d => d.worked))));
    return {
      ticks: Array.from({ length: max + 1 }, (_, i) => max - i),
      expectedPct: (EXPECTED_HOURS / max) * 100,
      edge: 50 / (days.length || 1),   // centre of the first/last column, in %
      dense: days.length > 10,
      days: days.map(d => ({ ...d, pct: (d.worked / max) * 100, value: hm(d.worked * 60) })),
    };
  });

  awayChart = computed(() => {
    const days = this.chartDays();
    const max = Math.max(1, Math.ceil(Math.max(0, ...days.map(d => d.away))));
    return {
      ticks: [5, 4, 3, 2, 1, 0].map(i => +((max * i) / 5).toFixed(1)),
      empty: days.every(d => !d.away),
      dense: days.length > 10,
      days: days.map(d => ({ ...d, pct: (d.away / max) * 100, value: hm(d.away * 60) })),
    };
  });

  // ---------- Export ----------
  exportWorkHours() {
    this.download('work-hours', ['Name', 'Man Days', 'Work Hours (Avg)'],
      this.rows().map(r => [r.name, r.manDays, r.working]));
  }

  exportSplit() {
    this.download('productivity-split', ['Name', 'Man Days', 'Over Worked', 'Healthy', 'Under Worked'],
      this.rows().map(r => [r.name, r.manDays, r.over, r.healthy, r.under]));
  }

  exportMetrics() {
    this.download('work-time-metrics', ['Name', 'Active Time (Avg)', 'Approved Away Time (Avg)', 'Idle Hours (Avg)'],
      this.rows().map(r => [r.name, r.working, r.approvedAway, r.idle]));
  }

  private download(name: string, header: string[], rows: (string | number)[][]) {
    const csv = [header, ...rows].map(r => r.map(c => `"${String(c).replace(/"/g, '""')}"`).join(',')).join('\n');
    const url = URL.createObjectURL(new Blob([csv], { type: 'text/csv' }));
    const a = document.createElement('a');
    a.href = url;
    a.download = `${name} ${this.periodLabel()}.csv`;
    a.click();
    URL.revokeObjectURL(url);
  }
}
