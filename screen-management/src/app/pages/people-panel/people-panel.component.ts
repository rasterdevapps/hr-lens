import { Component, inject, model, signal } from '@angular/core';
import { MatIconModule } from '@angular/material/icon';
import { MatTooltipModule } from '@angular/material/tooltip';
import { Employee, Selection, Team, TimeTrackerService } from '../dashboard/time-tracker.service';

export type { Selection } from '../dashboard/time-tracker.service';

type PanelTab = 'Employees' | 'Teams';

/** Employees / Teams picker shown to the left of the Time Tracker and Monitoring pages */
@Component({
  selector: 'app-people-panel',
  standalone: true,
  imports: [MatIconModule, MatTooltipModule],
  templateUrl: './people-panel.component.html',
  styleUrl: './people-panel.component.scss',
})
export class PeoplePanelComponent {
  private readonly data = inject(TimeTrackerService);

  selection = model.required<Selection>();

  // The server returns only the people the signed-in role may see
  readonly employees = this.data.employees;
  readonly teams = this.data.teams.map(t => ({ team: t, members: t.members }));
  readonly tabs: PanelTab[] = ['Employees', 'Teams'];

  tab = signal<PanelTab>('Employees');
  closedTeams = signal<ReadonlySet<string>>(new Set());

  initials(name: string) {
    return name.split(' ').map(p => p[0]).join('').slice(0, 2).toUpperCase();
  }

  isSelected(target: Employee | Team) {
    const s = this.selection();
    return s.kind === 'employee' ? s.employee === target : s.team === target;
  }

  selectEmployee(employee: Employee) { this.selection.set({ kind: 'employee', employee }); }
  selectTeam(team: Team) { this.selection.set({ kind: 'team', team }); }

  toggleTeam(name: string) {
    this.closedTeams.update(closed => {
      const next = new Set(closed);
      if (!next.delete(name)) next.add(name);
      return next;
    });
  }
}
