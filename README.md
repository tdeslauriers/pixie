# pixie

**Description:** A learning project to rebuild the gallery service that serves images for the family website.

**Name:** _pixie as in pics-y... because it serves pictures. Yup._ :man_shrugging:

## Components

This project uses several image processing `go` dependencies like `disintegration/imaging` and `xor-gate/goexif2` because I simply dont have that experience.  Down the line, I would like to study up and replace them with my own code, but these were used to get the site up and running.

Two types of peristance are used.  `min.io` is a locally hosted object storage, ie, the images files themselves, and a `mariadb` database to store metadata and permissions about the image files.