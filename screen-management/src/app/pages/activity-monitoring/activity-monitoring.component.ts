import { Component } from '@angular/core';
import { MatCardModule } from '@angular/material/card';
import { MatIconModule } from '@angular/material/icon';
import { MatTableModule } from '@angular/material/table';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatSelectModule } from '@angular/material/select';
import { MatDatepickerModule } from '@angular/material/datepicker';
import { MatNativeDateModule } from '@angular/material/core';
import { MatInputModule } from '@angular/material/input';
import { MatProgressBarModule } from '@angular/material/progress-bar';

interface Activity { time: string; employee: string; application: string; title: string; duration: string; productivity: number; }

@Component({
  selector: 'app-activity-monitoring',
  standalone: true,
  imports: [MatCardModule, MatIconModule, MatTableModule, MatFormFieldModule, MatSelectModule,
    MatDatepickerModule, MatNativeDateModule, MatInputModule, MatProgressBarModule],
  templateUrl: './activity-monitoring.component.html',
  styleUrl: './activity-monitoring.component.scss',
})
export class ActivityMonitoringComponent {
  employees = ['All Employees', 'Alice Johnson', 'Bob Smith', 'Carol Lee', 'David Kim'];
  columns = ['time', 'employee', 'application', 'title', 'duration', 'productivity'];
  activities: Activity[] = [
    { time: '09:02', employee: 'Alice Johnson', application: 'VS Code', title: 'screen-management – shell.component.ts', duration: '48m', productivity: 92 },
    { time: '09:50', employee: 'Bob Smith', application: 'Chrome', title: 'Jira – Sprint Board', duration: '22m', productivity: 75 },
    { time: '10:12', employee: 'Carol Lee', application: 'Slack', title: '#team-frontend', duration: '15m', productivity: 60 },
    { time: '10:30', employee: 'Alice Johnson', application: 'Figma', title: 'Dashboard mockups', duration: '35m', productivity: 88 },
    { time: '11:05', employee: 'David Kim', application: 'YouTube', title: 'Trending', duration: '12m', productivity: 10 },
  ];
}
