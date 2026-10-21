import { Component, input } from '@angular/core';
import { ReactiveFormsModule } from '@angular/forms';
import { provideNativeDateAdapter } from '@angular/material/core';
import { MatDatepickerModule } from '@angular/material/datepicker';
import { MatIconModule } from '@angular/material/icon';
import { MatMenuModule } from '@angular/material/menu';
import { PeriodFilter } from './period';

/** Page title, date navigation, time zone and Day / Week / Month / Date Range */
@Component({
  selector: 'app-period-toolbar',
  standalone: true,
  imports: [ReactiveFormsModule, MatDatepickerModule, MatIconModule, MatMenuModule],
  providers: [provideNativeDateAdapter()],
  template: `
    @let p = period();
    <h2 class="who" [title]="title()">{{ title() }}</h2>

    <!-- Pickers stay in the DOM so the range buttons can open them -->
    <input class="picker-input" [matDatepicker]="picker" [max]="p.today" [value]="p.date()"
           (dateChange)="$event.value && p.date.set($event.value)" tabindex="-1" aria-hidden="true">
    <mat-datepicker #picker [startView]="p.range() === 'Month' ? 'year' : 'month'"
                    (monthSelected)="p.monthPicked($event, picker)"></mat-datepicker>
    <mat-date-range-input class="picker-input" [formGroup]="p.rangeForm" [rangePicker]="rangePicker" [max]="p.today" aria-hidden="true">
      <input matStartDate formControlName="start" tabindex="-1">
      <input matEndDate formControlName="end" tabindex="-1">
    </mat-date-range-input>
    <mat-date-range-picker #rangePicker></mat-date-range-picker>

    <div class="date-nav">
      <button type="button" class="round" aria-label="Previous" (click)="p.shift(-1)"><mat-icon>chevron_left</mat-icon></button>
      <button type="button" class="date" (click)="p.openPicker(picker, rangePicker)">
        {{ p.label() }}
        <mat-icon>calendar_month</mat-icon>
      </button>
      <button type="button" class="round" aria-label="Next" [disabled]="p.atEnd()" (click)="p.shift(1)"><mat-icon>chevron_right</mat-icon></button>
      <button type="button" class="tz" [matMenuTriggerFor]="zoneMenu" aria-label="Time zone">
        {{ p.zone().code }} <mat-icon>expand_more</mat-icon>
      </button>
      <mat-menu #zoneMenu="matMenu">
        @for (z of p.zones; track z.code) {
          <button mat-menu-item (click)="p.zone.set(z)">
            <mat-icon>{{ p.zone() === z ? 'radio_button_checked' : 'radio_button_unchecked' }}</mat-icon>
            <span>{{ z.code }} · {{ z.label }}</span>
          </button>
        }
      </mat-menu>
    </div>

    <div class="ranges" role="group" aria-label="Range">
      @for (r of p.ranges; track r) {
        <button type="button" [class.active]="p.range() === r" [attr.aria-pressed]="p.range() === r"
                (click)="p.setRange(r, rangePicker)">{{ r }}</button>
      }
    </div>
    <ng-content />
  `,
  styleUrl: './period-toolbar.component.scss',
})
export class PeriodToolbarComponent {
  period = input.required<PeriodFilter>();
  title = input('');
}
