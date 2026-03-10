# Conventions

## Feature Names

Reasoning: Behavioral names describe what the system does, making specs scannable and searchable.

Rule: Feature names must be behavioral — describe the action or transformation, not the module.

## Test Names

Reasoning: Consistent naming makes tests discoverable and clarifies what each test verifies.

Rule: Use Test + camelCase with present tense verbs. Use underscores for negative/edge cases.
- ✅ TestSignalReturnAsString
- ✅ TestPage_PanicsOnNoView
- ❌ TestSignal (vague)
- ❌ TestSignal_ReturnAsString (inconsistent style)

## Test Comments

Reasoning: Explanatory comments are valuable when tests cover complex, non-obvious, or surprising behavior.

Rule: Add comments for complex error paths, edge cases, and surprising behavior. Skip for trivial tests.

## Behavioral Descriptions

Reasoning: Specs should describe observable behavior without implementation details. Simple terms ensure clarity.

Rule: No implementation details in behavior descriptions.

- ✅ A page with a view renders HTML when requested
- ❌ Route handler sets response status to 200 and writes HTML body (implementation detail)
