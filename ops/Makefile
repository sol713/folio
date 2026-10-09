.PHONY: test check integration doctor package
test:
	python3 -m unittest discover -s tests -v
check: test
	python3 -m py_compile ops.py
integration:
	python3 tests/integration.py --binary $(or $(BINARY),../dist/folio)
doctor:
	python3 ops.py doctor
package:
	python3 scripts/package.py
