import { Component, computed, effect, inject, input } from '@angular/core';
import { addDays, Employee, TimeTrackerService } from '../time-tracker.service';

type Metric = 'working' | 'productive' | 'idle';

interface Cell { title: string; level: number; }   // level 0 = no time, 1–10 = less → more
interface Month { label: string; column: number; }

const LEVELS = 10;
const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
const METRICS: { key: Metric; label: string; color: string }[] = [
  { key: 'working', label: 'Working Time', color: '#4f46e5' },
  { key: 'productive', label: 'Productive Time', color: '#16a34a' },
  { key: 'idle', label: 'Away Time', color: '#64748b' },
];

function duration(minutes: number) {
  return `${Math.floor(minutes / 60)}h ${Math.round(minutes % 60)}m`;
}

@Component({
  selector: 'app-heat-maps',
  standalone: true,
  templateUrl: './heat-maps.component.html',
  styleUrl: './heat-maps.component.scss',
})
export class HeatMapsComponent {
  private readonly data = inject(TimeTrackerService);

  members = input.required<Employee[]>();

  readonly year = new Date().getFullYear();
  readonly weekdays = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
  readonly legend = Array.from({ length: LEVELS }, (_, i) => (i + 1) / LEVELS);

  // Blank cells before 1 Jan so the first column starts on Sunday
  readonly offset = Array.from({ length: new Date(this.year, 0, 1).getDay() });
  readonly months: Month[] = MONTHS.map((label, m) => {
    const first = new Date(this.year, m, 1);
    const dayOfYear = Math.round((first.getTime() - new Date(this.year, 0, 1).getTime()) / 86400000);
    return { label, column: Math.floor((dayOfYear + this.offset.length) / 7) + 1 };
  });

  /** Fetch the whole year so far */
  private readonly loadYear = effect(() => {
    const dates: Date[] = [];
    for (let d = new Date(this.year, 0, 1); d <= new Date(); d = addDays(d, 1)) dates.push(d);
    this.data.load(this.members(), dates);
  }, { allowSignalWrites: true });

  // Minutes per day, summed over the selected people
  private days = computed(() => {
    const now = new Date(), days = [];
    for (let d = new Date(this.year, 0, 1); d.getFullYear() === this.year; d = addDays(d, 1)) {
      const list = this.data.present(this.members(), [d]);
      const total = (key: Metric) => list.reduce((a, r) => a + r[key], 0);
      days.push({ date: d, future: d > now, working: total('working'), productive: total('productive'), idle: total('idle') });
    }
    return days;
  });

  maps = computed(() => {
    const days = this.days();
    return METRICS.map(m => {
      const max = Math.max(1, ...days.map(d => d[m.key]));
      return {
        ...m,
        total: duration(days.reduce((a, d) => a + d[m.key], 0)),
        cells: days.map((d): Cell => ({
          title: `${d.date.getDate()} ${MONTHS[d.date.getMonth()]} ${this.year}` + (d.future ? '' : ` · ${duration(d[m.key])}`),
          level: Math.ceil((d[m.key] / max) * LEVELS),
        })),
      };
    });
  });
}
