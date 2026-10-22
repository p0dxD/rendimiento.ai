# Miembros y funciones {#members-and-roles}

Las personas entran a rendimiento con GitHub, y solo pasan los **miembros**. Cada miembro tiene una **función** que dice qué puede hacer; cada ruta de la API la comprueba, así que la interfaz y la API coinciden.

| Función | Puede |
|---|---|
| **Invitado** | ver todo: las aplicaciones, las ejecuciones y sus registros, las versiones, el [Universo](universe.md), la [Ruta](ruta.md) y los encargos |
| **Colaborador** | además: lanzar CI, cancelar ejecuciones, escribir en la Ruta, [pedirles cosas a los agentes](agents.md#encargos) y revisar lo que entregan, editar páginas del [Mercado](mercado.md), encender Renovate en una aplicación |
| **Administrador** | además: desplegar y revertir, leer y cambiar secretos, incorporar y quitar aplicaciones, y manejar complementos, llaves de agentes, miembros y la actualización de la plataforma |

## Agregar a alguien {#adding-someone}

En **Miembros** (su nombre en la barra de arriba), un administrador escribe el **usuario de GitHub** de la persona y su función. Desde ese momento puede entrar, en la misma dirección. Cambiar una función o quitar a alguien cuenta desde su siguiente clic; a quien se quita se le cierra la sesión de inmediato. Una organización siempre conserva al menos un administrador.

## Los primeros administradores {#the-first-administrators}

Una plataforma nueva no tiene miembros, así que el ajuste `ALLOWED_USERS` nombra a los primeros administradores. Se agregan una sola vez, mientras no hay miembros; después, las personas se manejan en la página Miembros y el ajuste ya no cuenta.

## Organizaciones {#organizations}

Todo pertenece a una **organización**: sus aplicaciones (con sus ejecuciones, versiones y solicitudes), su Ruta, sus encargos, sus llaves de agentes y sus miembros. Las personas de una organización nunca ven lo de otra: las listas no lo incluyen y sus páginas responden *no existe*. Una persona puede pertenecer a varias, con una función en cada una, y cambia con el selector de arriba; cuenta la función en la organización en la que está trabajando.

La **organización de la plataforma** (la primera) es la que la opera. Sus administradores son los **operadores**: solo ellos ven las páginas del clúster (Entorno, Complementos, Servicios, Problemas, el Mercado), actualizan la plataforma y, por ahora, crean organizaciones (en Miembros → Organizaciones) e incorporan aplicaciones, en la organización en la que estén trabajando. Una llave de agente pertenece a la organización en la que se creó, y sus herramientas solo ven esa organización.

Hoy todas las organizaciones comparten el clúster de la plataforma. Las organizaciones en un clúster virtual dedicado, o en su propio clúster en cualquier nube, llegan después.
