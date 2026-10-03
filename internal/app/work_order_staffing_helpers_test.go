package app

// waitWorkOrderStaffing ждёт фоновый подбор состава: тесты читают наряд сразу
// после хода Мастера, а состав дописывает задача после него.
func (a *App) waitWorkOrderStaffing() { a.staffing.wg.Wait() }
