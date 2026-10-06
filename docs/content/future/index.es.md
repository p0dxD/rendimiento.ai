# Hacerlo crecer

rendimiento funciona hoy para una persona y un clúster de Raspberry Pi. Esta parte trata de lo que sigue:

1. **[Escalar](scaling.md)**: dónde llega cada parte a su límite, y qué cambiar cuando pase: las construcciones, la cola, los controladores, la API, el almacenamiento y varios clústeres.
2. **[Hoja de ruta](roadmap.md)**: las funciones en un orden sensato, cada una con los archivos que toca.
3. **[Código abierto](open-source.md)**: licencia, forma de contribuir, integración continua, versiones, imágenes para varias arquitecturas, instalación para otras personas y gobierno del proyecto.

```mermaid
flowchart LR
    now[Hoy<br/>1 clúster, 1 usuario,<br/>1 réplica] --> team[Un equipo<br/>roles, bitácora de auditoría,<br/>plataforma de alta disponibilidad]
    team --> multi[Muchos clústeres<br/>agentes, destinos en la nube]
    now --> oss[Código abierto<br/>se instala donde sea,<br/>contribuidores]
    oss --> multi
```
