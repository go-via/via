## ADR

- Support Multiple Rooms
Not single chat room toy problem.

- Rooms are generic
They know nothing of their data. Just store it. Reusable for different usecases.

- Server controls push frequency
Debounce to every 400ms, if dirty.
