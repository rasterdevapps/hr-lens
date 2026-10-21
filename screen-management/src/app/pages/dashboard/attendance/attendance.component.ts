import { Component, computed, inject, input, signal } from '@angular/core';
import { DatePipe } from '@angular/common';
import { MatIconModule } from '@angular/material/icon';
import { MatTooltipModule } from '@angular/material/tooltip';
import { DayRecord, Employee, hm, Segment, TimeTrackerService } from '../time-tracker.service';

const DAY_MIN = 24 * 60;
const TIMELINE_START = 7 * 60;   // timeline runs 7:00 AM → 7:00 AM next day

@Component({
  selector: 'app-attendance',
  standalone: true,
  imports: [DatePipe, MatIconModule, MatTooltipModule],
  templateUrl: './attendance.component.html',
  styleUrl: './attendance.component.scss',
})
export class AttendanceComponent {
  private readonly data = inject(TimeTrackerService);

  members = input.required<Employee[]>();
  dates = input.required<Date[]>();
  tzShift = input(0);   // minutes to add to the recorded (IST) times

  ascending = signal(false);
  private picked = signal<number | null>(null);
  readonly hm = hm;

  // 7:00 AM, 9:00 AM … 7:00 AM
  readonly ticks = Array.from({ length: 13 }, (_, i) => {
    const h = (7 + i * 2) % 24;
    return { time: `${h % 12 || 12}:00`, period: h < 12 ? 'AM' : 'PM', left: (i / 12) * 100 };
  });

  /** Attendance is per person: with a team selected, one member is shown at a time */
  employee = computed(() => this.members().find(e => e.id === this.picked()) ?? this.members()[0]);

  private days = computed(() => {
    const e = this.employee();
    return e ? this.dates().map(d => this.data.day(e, d)) : [];
  });

  rows = computed(() => (this.ascending() ? this.days() : [...this.days()].reverse()));

  stats = computed(() => {
    const sum = (key: 'working' | 'productive' | 'idle' | 'online' | 'approvedAway') => hm(this.days().reduce((a, d) => a + d[key], 0));
    return [
      { label: 'Total Working Time', value: sum('working'), tone: 'teal' },
      { label: 'Time Spent', value: sum('online'), tone: 'blue' },
      { label: 'Productive Time', value: sum('productive'), tone: 'green' },
      { label: 'Idle Time', value: sum('idle'), tone: 'amber' },
      { label: 'Away Time', value: sum('approvedAway'), tone: 'grey' },
    ];
  });

  pick(e: Employee) { this.picked.set(e.id); }

  private local(minutes: number) {
    return (((minutes + this.tzShift()) % DAY_MIN) + DAY_MIN) % DAY_MIN;
  }

  clock(minutes: number) { return hm(this.local(minutes)); }

  /** Why the day is Late: the shift's last on-time minute, in the shown time zone */
  lateTip(d: DayRecord) {
    const [h, m] = d.lateAfter.split(':').map(Number);
    return `On time until ${this.clock(h * 60 + m)} for this shift`;
  }

  left(s: Segment) {
    return (((this.local(s.start) - TIMELINE_START + DAY_MIN) % DAY_MIN) / DAY_MIN) * 100;
  }

  width(s: Segment) { return (s.length / DAY_MIN) * 100; }

  tip(s: Segment) {
    return `${s.idle ? 'Idle' : 'Working'} ${this.clock(s.start)} – ${this.clock(s.start + s.length)}`;
  }

  trackDay(d: DayRecord) { return d.date.getTime(); }
}
