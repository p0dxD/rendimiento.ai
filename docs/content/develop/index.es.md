# Desarrollarlo

Todo lo que necesita para cambiar rendimiento con confianza:

1. **[Go para este código](go.md)**: el lenguaje, enseñado con el propio código de este repositorio: paquetes, tipos, interfaces, errores, contextos, concurrencia, incrustación, genéricos y pruebas.
2. **[Preparar y construir](setup.md)**: las herramientas, la organización del repositorio, los objetivos de `make`, cómo ejecutarlo y cómo trabajar en la interfaz.
3. **[Recetas para cambios comunes](recipes.md)**: paso a paso: un campo nuevo en `rendimiento.yaml`, una ruta de la API con su página en la interfaz, un complemento del catálogo, una comprobación del entorno, un proveedor de DNS, una migración de la base de datos y un tipo nuevo de paso.
4. **[Pruebas](testing.md)**: los tipos de pruebas, dónde corren y cómo escribir nuevas.
5. **[Patrones de diseño](patterns.md)**: los patrones en uso, por qué se eligió cada uno y dónde encontrarlo.
6. **[Reestructuración y salud del código](refactoring.md)**: qué mejorar después en el propio código, y cómo.

Las reglas de oro:

- **Corra las pruebas en un nodo trabajador**, nunca en `main`: `make test-remote`.
- **Vuelva a generar después de cambiar tipos**: `make generate` (los objetivos de pruebas lo hacen).
- **Mantenga el libro en la misma solicitud de incorporación** que el cambio que describe.
