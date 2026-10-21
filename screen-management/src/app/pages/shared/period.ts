import { computed, signal } from '@angular/core';
import { formatDate } from '@angular/common';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormControl, FormGroup } from '@angular/forms';
import { MatDatepicker, MatDateRangePicker } from '@angular/material/datepicker';
import { addDays, startOfDay } from '../dashboard/time-tracker.service';

export type Range = 'Day' | 'Week' | 'Month' | 'Date Range';

export interface Zone { code: string; label: string; offset: string; tz: string; }

/** Display zones: the IANA name the server counts days in; `offset` is only a label default, use pipeOffset() to format */
export const ZONES: Zone[] = [
  { code: 'IST', label: 'India (UTC+5:30)', offset: '+0530', tz: 'Asia/Kolkata' },
  { code: 'GST', label: 'Gulf (UTC+4:00)', offset: '+0400', tz: 'Asia/Dubai' },
  { code: 'UTC', label: 'UTC', offset: '+0000', tz: 'UTC' },
  { code: 'EST', label: 'US Eastern', offset: '-0500', tz: 'America/New_York' },
  { code: 'PST', label: 'US Pacific', offset: '-0800', tz: 'America/Los_Angeles' },
];


/** Minutes east of UTC that an IANA zone is at a given moment (so US zones follow daylight saving) */
export function zoneOffsetMinutes(tz: string, at: Date): number {
  const parts = new Intl.DateTimeFormat('en-US', { timeZone: tz, hourCycle: 'h23', year: 'numeric', month: 'numeric', day: 'numeric', hour: 'numeric', minute: 'numeric', second: 'numeric' })
    .formatToParts(at).reduce((o, p) => ({ ...o, [p.type]: p.value }), {} as Record<string, string>);
  const asUtc = Date.UTC(+parts['year'], +parts['month'] - 1, +parts['day'], +parts['hour'], +parts['minute'], +parts['second']);
  return Math.round((asUtc - at.getTime()) / 60000);
}

/** The same offset as "+0530" for Angular's formatDate */
export function pipeOffset(tz: string, at: Date): string {
  const m = zoneOffsetMinutes(tz, at), a = Math.abs(m);
  return `${m < 0 ? '-' : '+'}${String(Math.floor(a / 60)).padStart(2, '0')}${String(a % 60).padStart(2, '0')}`;
}

const DAY_MS = 86400000;
const fmt = (d: Date, pattern: string) => formatDate(d, pattern, 'en-US');
export const isoDate = (d: Date) => fmt(d, 'yyyy-MM-dd');

/**
 * The Day / Week / Month / Date Range period and time zone behind a page's toolbar.
 * Create it in a field initializer (it needs an injection context).
 */
export class PeriodFilter {
  readonly ranges: Range[] = ['Day', 'Week', 'Month', 'Date Range'];
  readonly zones = ZONES;
  readonly today = startOfDay(new Date());

  range = signal<Range>('Day');
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

  /** First and last day of the period (the end may be in the future) */
  readonly period = computed(() => {
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

  /** The period as the API takes it, ending no later than today */
  readonly apiRange = computed(() => {
    const { from, to } = this.period();
    return { from: isoDate(from), to: isoDate(to > this.today ? this.today : to), tz: this.zone().tz };
  });

  readonly atEnd = computed(() => this.period().to >= this.today);

  readonly label = computed(() => {
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
}
