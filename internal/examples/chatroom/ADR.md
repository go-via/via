## ADR

- Support Multiple Rooms
Not single chat room toy problem.

- Rooms are generic
They knew nothing of their data.

- Server controls push frequency
Debounce to every 500ms, if dirty.

- Rooms don't know about members.
This is a separate concern.


Test cases:
- DogDoor: Enter/Leave repeated. Need to de-register callbacks and free context. No cookie to bind to user info.
- 1000 Monkeys: concurrent clients. Playwright. JS to submit text.